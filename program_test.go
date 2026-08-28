package micropython

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const handlerSrc = `
def score(row):
    total = row["a"] * 2 + row["b"]
    return {"id": row["id"], "score": total, "ok": total > 10}

def boom():
    raise ValueError("nope")
`

const counterSrc = `
_calls = 0

def name():
    return %q

def bump():
    global _calls
    _calls += 1
    return _calls
`

const spinSrc = `
def spin():
    while True:
        pass

def spin_swallowing():
    while True:
        try:
            pass
        except:
            pass

def double(n):
    return n * 2
`

func newProgram(t *testing.T) *Program {
	t.Helper()
	p, err := CompileSource(t.Context(), handlerSrc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestProgramCall(t *testing.T) {
	p := newProgram(t)

	got, err := p.Call(t.Context(), "score", map[string]any{"id": "r-1", "a": 4, "b": 5})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": "r-1", "score": int64(13), "ok": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("score = %#v, want %#v", got, want)
	}
}

func TestProgramError(t *testing.T) {
	p := newProgram(t)

	var exc *PythonError
	if _, err := p.Call(t.Context(), "boom"); !errors.As(err, &exc) {
		t.Fatalf("got %v (%T), want *Exception", err, err)
	} else if exc.Type() != "ValueError" {
		t.Errorf("Type = %q, want ValueError", exc.Type())
	}

	if _, err := p.Call(t.Context(), "score", map[string]any{"id": "x", "a": 1, "b": 1}); err != nil {
		t.Errorf("after error: %v", err)
	}
}

func TestProgramConcurrent(t *testing.T) {
	p := newProgram(t)

	const goroutines, each = 8, 25

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*each)

	for g := range goroutines {
		wg.Go(func() {
			for i := range each {
				row := map[string]any{"id": "r", "a": int64(g), "b": int64(i)}
				got, err := p.Call(t.Context(), "score", row)
				if err != nil {
					errs <- err
					return
				}
				m, ok := got.(map[string]any)
				if !ok {
					errs <- errors.New("not a dict")
					return
				}
				if want := int64(g)*2 + int64(i); m["score"] != want {
					errs <- errors.New("wrong score")
					return
				}
			}
		})
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestProgramClose(t *testing.T) {
	p, err := CompileSource(t.Context(), handlerSrc)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Call(t.Context(), "score", map[string]any{"id": "x", "a": 1, "b": 1}); !errors.Is(err, ErrClosed) {
		t.Errorf("after Close: %v, want ErrClosed", err)
	}
}

func TestPoolBounded(t *testing.T) {
	p, err := CompileSource(t.Context(), "def f(n):\n    return n\n", WithPoolSize(12))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	max := p.maxIdle

	const burst = 64
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := range burst {
		wg.Go(func() {
			<-start // all in flight at once, forcing the pool to grow
			if _, err := p.Call(t.Context(), "f", int64(i)); err != nil {
				t.Error(err)
			}
		})
	}
	close(start)
	wg.Wait()

	p.mu.Lock()
	idle, capacity := len(p.free), cap(p.free)
	p.mu.Unlock()

	t.Logf("after a burst of %d: idle=%d cap=%d maxIdle=%d", burst, idle, capacity, max)
	if idle > max {
		t.Errorf("pool kept %d idle interpreters, want at most %d", idle, max)
	}

	if got, err := p.Call(t.Context(), "f", int64(7)); err != nil || got != int64(7) {
		t.Errorf("after burst: %#v, %v", got, err)
	}
}

func TestProgramCallsDoNotLeakState(t *testing.T) {
	p, err := CompileSource(t.Context(), `
counter = 0

def bump():
    global counter
    counter += 1
    return counter

def stash(v):
    global leaked
    leaked = v
    return v

def peek():
    return globals().get("leaked", "clean")
`)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	for i := range 5 {
		got, err := p.Call(t.Context(), "bump")
		if err != nil {
			t.Fatal(err)
		}
		if got != int64(1) {
			t.Fatalf("call %d: counter = %v, want 1 -- state survived the pool", i, got)
		}
	}

	if _, err := p.Call(t.Context(), "stash", "dirty"); err != nil {
		t.Fatal(err)
	}
	got, err := p.Call(t.Context(), "peek")
	if err != nil {
		t.Fatal(err)
	}
	if got != "clean" {
		t.Errorf("peek = %v, want \"clean\" -- a global leaked through the pool", got)
	}
}

func TestProgramsAreIndependent(t *testing.T) {
	const n = 4

	programs := make([]*Program, n)
	for i := range programs {
		p, err := CompileSource(t.Context(), fmt.Sprintf(counterSrc, fmt.Sprintf("p%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		programs[i] = p
	}

	for i, p := range programs {
		got, err := p.Call(t.Context(), "name")
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("p%d", i); got != want {
			t.Errorf("program %d: name() = %v, want %q", i, got, want)
		}
	}

	if _, err := programs[0].Call(t.Context(), "exec", "extra = 1"); err == nil {
		t.Log("note: exec reachable as a global")
	}
	for i, p := range programs {
		if _, err := p.Call(t.Context(), "no_such_function"); err == nil {
			t.Errorf("program %d resolved a name that does not exist", i)
		}
	}
}

func TestProgramCloseIsLocal(t *testing.T) {
	a, err := CompileSource(t.Context(), fmt.Sprintf(counterSrc, "a"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileSource(t.Context(), fmt.Sprintf(counterSrc, "b"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Call(t.Context(), "name"); err == nil {
		t.Error("a closed Program still served a call")
	}

	got, err := b.Call(t.Context(), "name")
	if err != nil || got != "b" {
		t.Errorf("after closing a: b.name() = %v, %v", got, err)
	}
}

func TestProgramsConcurrentAcrossPrograms(t *testing.T) {
	const programs, goroutines, calls = 4, 4, 20

	var wg sync.WaitGroup
	errs := make(chan error, programs*goroutines)

	for i := range programs {
		p, err := CompileSource(t.Context(), fmt.Sprintf(counterSrc, fmt.Sprintf("p%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()

		want := fmt.Sprintf("p%d", i)
		for range goroutines {
			wg.Add(1)
			go func(p *Program, want string) {
				defer wg.Done()
				for range calls {
					got, err := p.Call(t.Context(), "name")
					if err != nil {
						errs <- err
						return
					}
					if got != want {
						errs <- fmt.Errorf("got %v, want %q -- interpreters crossed", got, want)
						return
					}
				}
			}(p, want)
		}
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestProgramRepeatedCalls(t *testing.T) {
	p, err := CompileSource(t.Context(), fmt.Sprintf(counterSrc, "x"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	for i := range 50 {
		got, err := p.Call(t.Context(), "bump")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if got != int64(1) {
			t.Fatalf("call %d: bump() = %v, want 1 -- state carried over", i, got)
		}
	}
}

func TestProgramRealisticHandler(t *testing.T) {
	p, err := CompileSource(t.Context(), `
import json
import re

_email = re.compile(r"^[\w.]+@[\w.]+$")

def handle(request):
    body = json.loads(request["body"])
    errors = []

    if not _email.match(body.get("email", "")):
        errors.append("email")
    if not isinstance(body.get("age"), int) or body["age"] < 0:
        errors.append("age")

    if errors:
        return {"ok": False, "invalid": errors}
    return {"ok": True, "email": body["email"], "age": body["age"]}
`)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{"valid", `{"email": "a.b@example.com", "age": 30}`, true},
		{"bad email", `{"email": "nope", "age": 30}`, false},
		{"negative age", `{"email": "a@b.com", "age": -1}`, false},
		{"missing fields", `{}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.Call(t.Context(), "handle", map[string]any{"body": tt.body})
			if err != nil {
				t.Fatalf("handle: %v", err)
			}
			m, ok := got.(map[string]any)
			if !ok {
				t.Fatalf("handle returned %#v (%T), want a dict", got, got)
			}
			if m["ok"] != tt.ok {
				t.Errorf("ok = %v, want %v (got %#v)", m["ok"], tt.ok, m)
			}
		})
	}

	if _, err := p.Call(t.Context(), "handle", Of(map[string]any{"body": "{not json"})); err == nil {
		t.Error("malformed JSON was accepted")
	}
}

func TestCompileRejects(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"syntax error", "def broken(:\n", "SyntaxError"},
		{"raises at import time", "raise ValueError('boom')\n", "ValueError"},
		{"name error at import time", "undefined_name()\n", "NameError"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := CompileSource(t.Context(), tt.src)
			if err == nil {
				p.Close()
				t.Fatalf("Compile accepted %q", tt.src)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("CompileSource(%q) = %v, want it to mention %s", tt.src, err, tt.want)
			}
		})
	}
}

func spinner(t *testing.T) *Program {
	t.Helper()
	p, err := CompileSource(context.Background(), spinSrc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestCancelDeadline(t *testing.T) {
	p := spinner(t)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := p.Call(ctx, "spin")
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("spin() = %v, want DeadlineExceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("took %v to stop, want promptly after the deadline", elapsed)
	}

	if got, err := p.Call(context.Background(), "double", int64(21)); err != nil || got != int64(42) {
		t.Errorf("after cancellation: %#v, %v", got, err)
	}
}

func TestCancelBeforeCall(t *testing.T) {
	p := spinner(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.Call(ctx, "spin"); !errors.Is(err, context.Canceled) {
		t.Fatalf("spin() = %v, want Canceled", err)
	}
	if got, err := p.Call(context.Background(), "double", int64(1)); err != nil || got != int64(2) {
		t.Errorf("after a pre-cancelled call: %#v, %v", got, err)
	}
}

func TestCancelIsNotSwallowedByBareExcept(t *testing.T) {
	p := spinner(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := p.Call(ctx, "spin_swallowing")
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("spin_swallowing() = %v, want DeadlineExceeded", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a guest catching KeyboardInterrupt outran cancellation")
	}
}

func TestCancelIsPerCall(t *testing.T) {
	p, err := CompileSource(context.Background(), `
def work(n):
    total = 0
    for i in range(n):
        total += i
    return total

def spin():
    while True:
        pass
`)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	long := make(chan error, 1)
	go func() {
		_, err := p.Call(context.Background(), "work", int64(2_000_000))
		long <- err
	}()

	// Give the long call time to be in flight, then cancel a different one.
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := p.Call(ctx, "spin"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("spin() = %v, want DeadlineExceeded", err)
	}

	select {
	case err := <-long:
		if err != nil {
			t.Errorf("the long call was cut short by another call's deadline: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the long call never finished")
	}
}

func TestProgramHostFunc(t *testing.T) {
	ctx := context.Background()

	rates := map[string]float64{"EUR": 1.09, "GBP": 1.27}

	var calls atomic.Int64
	usd := func(args []any) (any, error) {
		calls.Add(1)
		code := args[0].(string)
		rate, ok := rates[code]
		if !ok {
			return nil, Raise("KeyError", code)
		}
		return rate * float64(args[1].(int64)), nil
	}

	// The pool holds one idle instance, so concurrent calls have to restore
	// fresh ones from the snapshot. That is the path the binding must survive.
	p, err := CompileSource(ctx, `
def convert(code, amount):
    return round(usd(code, amount), 2)
`,
		WithHostFunc("usd", usd),
		WithPoolSize(5),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	got, err := p.Call(ctx, "convert", "EUR", 100)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got != 109.0 {
		t.Fatalf("got %v, want 109.0", got)
	}

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := p.Call(ctx, "convert", "GBP", 10)
			if err != nil {
				errs[i] = err
				return
			}
			if v != 12.7 {
				errs[i] = fmt.Errorf("got %v, want 12.7", v)
			}
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}

	if want := int64(n + 1); calls.Load() != want {
		t.Errorf("host function ran %d times, want %d", calls.Load(), want)
	}
}

func TestProgramHostFuncAtModuleLevel(t *testing.T) {
	ctx := context.Background()

	var calls atomic.Int64
	p, err := CompileSource(ctx, `
LIMIT = fetch_limit()

def within(n):
    return n <= LIMIT
`,
		WithHostFunc("fetch_limit", func([]any) (any, error) {
			calls.Add(1)
			return 5, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	for _, tc := range []struct {
		n    int
		want bool
	}{{3, true}, {5, true}, {6, false}} {
		got, err := p.Call(ctx, "within", tc.n)
		if err != nil {
			t.Fatalf("within(%d): %v", tc.n, err)
		}
		if got != tc.want {
			t.Errorf("within(%d) = %v, want %v", tc.n, got, tc.want)
		}
	}

	if calls.Load() != 1 {
		t.Errorf("fetch_limit ran %d times, want 1 (it should be in the snapshot)", calls.Load())
	}
}

func TestProgramHostFuncError(t *testing.T) {
	ctx := context.Background()

	p, err := CompileSource(ctx, `
def lookup(code):
    try:
        return rate(code)
    except KeyError:
        return None
`,
		WithHostFunc("rate", func(args []any) (any, error) {
			return nil, Raise("KeyError", args[0].(string))
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	got, err := p.Call(ctx, "lookup", "ZAR")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}

	if _, err := p.Call(ctx, "rate", "ZAR"); err == nil {
		t.Fatal("want an error")
	} else {
		var exc *PythonError
		if !errors.As(err, &exc) {
			t.Fatalf("want *PythonError, got %T", err)
		}
		if exc.Type() != "KeyError" {
			t.Errorf("got %s, want KeyError", exc.Type())
		}
	}

	if _, err := p.Call(ctx, "lookup", "ZAR"); err != nil {
		t.Fatalf("after error: %v", err)
	}
}
