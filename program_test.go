package micropython

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
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

	p, err := NewProgram(t.Context(), WithSource(handlerSrc))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { p.Close() })

	return p
}

func TestProgramCall(t *testing.T) {
	p := newProgram(t)

	got, err := progCall(t.Context(), p, "score", map[string]any{"id": "r-1", "a": 4, "b": 5})
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]any{"id": "r-1", "score": int64(13), "ok": true}
	if !reflect.DeepEqual(got.Export(), want) {
		t.Errorf("score = %#v, want %#v", got, want)
	}
}

func TestProgramError(t *testing.T) {
	p := newProgram(t)

	var exc *PythonError
	if _, err := progCall(t.Context(), p, "boom"); !errors.As(err, &exc) {
		t.Fatalf("got %v (%T), want *Exception", err, err)
	} else if exc.Type() != "ValueError" {
		t.Errorf("Type = %q, want ValueError", exc.Type())
	}

	if _, err := progCall(t.Context(), p, "score", map[string]any{"id": "x", "a": 1, "b": 1}); err != nil {
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

				got, err := progCall(t.Context(), p, "score", row)
				if err != nil {
					errs <- err
					return
				}

				m, ok := got.Export().(map[string]any)
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
	p, err := NewProgram(t.Context(), WithSource(handlerSrc))
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := progCall(t.Context(), p, "score", map[string]any{"id": "x", "a": 1, "b": 1}); !errors.Is(err, ErrClosed) {
		t.Errorf("after Close: %v, want ErrClosed", err)
	}
}

func TestPoolBounded(t *testing.T) {
	p, err := NewProgram(t.Context(), WithSource("def f(n):\n    return n\n"), WithMaxIdle(12))
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

			if _, err := progCall(t.Context(), p, "f", int64(i)); err != nil {
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

	if got, err := progCall(t.Context(), p, "f", int64(7)); err != nil || got.Export() != int64(7) {
		t.Errorf("after burst: %#v, %v", got, err)
	}
}

func TestProgramCallsDoNotLeakState(t *testing.T) {
	p, err := NewProgram(t.Context(), WithSource(`
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
`))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	for i := range 5 {
		got, err := progCall(t.Context(), p, "bump")
		if err != nil {
			t.Fatal(err)
		}

		if got.Export() != int64(1) {
			t.Fatalf("call %d: counter = %v, want 1 -- state survived the pool", i, got)
		}
	}

	if _, err := progCall(t.Context(), p, "stash", "dirty"); err != nil {
		t.Fatal(err)
	}

	got, err := progCall(t.Context(), p, "peek")
	if err != nil {
		t.Fatal(err)
	}

	if got.Export() != "clean" {
		t.Errorf("peek = %v, want \"clean\" -- a global leaked through the pool", got)
	}
}

func TestProgramsAreIndependent(t *testing.T) {
	const n = 4

	programs := make([]*Program, n)
	for i := range programs {
		p, err := NewProgram(t.Context(), WithSource(fmt.Sprintf(counterSrc, fmt.Sprintf("p%d", i))))
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()

		programs[i] = p
	}

	for i, p := range programs {
		got, err := progCall(t.Context(), p, "name")
		if err != nil {
			t.Fatal(err)
		}

		if want := fmt.Sprintf("p%d", i); got.Export() != want {
			t.Errorf("program %d: name() = %v, want %q", i, got, want)
		}
	}

	if _, err := progCall(t.Context(), programs[0], "exec", "extra = 1"); err == nil {
		t.Log("note: exec reachable as a global")
	}

	for i, p := range programs {
		if _, err := progCall(t.Context(), p, "no_such_function"); err == nil {
			t.Errorf("program %d resolved a name that does not exist", i)
		}
	}
}

func TestProgramCloseIsLocal(t *testing.T) {
	a, err := NewProgram(t.Context(), WithSource(fmt.Sprintf(counterSrc, "a")))
	if err != nil {
		t.Fatal(err)
	}

	b, err := NewProgram(t.Context(), WithSource(fmt.Sprintf(counterSrc, "b")))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := progCall(t.Context(), a, "name"); err == nil {
		t.Error("a closed Program still served a call")
	}

	got, err := progCall(t.Context(), b, "name")
	if err != nil || got.Export() != "b" {
		t.Errorf("after closing a: b.name() = %v, %v", got, err)
	}
}

func TestProgramsConcurrentAcrossPrograms(t *testing.T) {
	const programs, goroutines, calls = 4, 4, 20

	var wg sync.WaitGroup

	errs := make(chan error, programs*goroutines)

	for i := range programs {
		p, err := NewProgram(t.Context(), WithSource(fmt.Sprintf(counterSrc, fmt.Sprintf("p%d", i))))
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
					got, err := progCall(t.Context(), p, "name")
					if err != nil {
						errs <- err
						return
					}

					if got.Export() != want {
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
	p, err := NewProgram(t.Context(), WithSource(fmt.Sprintf(counterSrc, "x")))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	for i := range 50 {
		got, err := progCall(t.Context(), p, "bump")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}

		if got.Export() != int64(1) {
			t.Fatalf("call %d: bump() = %v, want 1 -- state carried over", i, got)
		}
	}
}

func TestProgramRealisticHandler(t *testing.T) {
	p, err := NewProgram(t.Context(), WithSource(`
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
`))
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
			got, err := progCall(t.Context(), p, "handle", map[string]any{"body": tt.body})
			if err != nil {
				t.Fatalf("handle: %v", err)
			}

			m, ok := got.Export().(map[string]any)
			if !ok {
				t.Fatalf("handle returned %#v (%T), want a dict", got, got)
			}

			if m["ok"] != tt.ok {
				t.Errorf("ok = %v, want %v (got %#v)", m["ok"], tt.ok, m)
			}
		})
	}

	if _, err := progCall(t.Context(), p, "handle", Of(map[string]any{"body": "{not json"})); err == nil {
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
			p, err := NewProgram(t.Context(), WithSource(tt.src))
			if err == nil {
				p.Close()
				t.Fatalf("Compile accepted %q", tt.src)
			}

			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Compile(%q) = %v, want it to mention %s", tt.src, err, tt.want)
			}
		})
	}
}

func spinner(t *testing.T) *Program {
	t.Helper()

	p, err := NewProgram(context.Background(), WithSource(spinSrc))
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
	_, err := progCall(ctx, p, "spin")
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("spin() = %v, want DeadlineExceeded", err)
	}

	if elapsed > 5*time.Second {
		t.Errorf("took %v to stop, want promptly after the deadline", elapsed)
	}

	if got, err := progCall(context.Background(), p, "double", int64(21)); err != nil || got.Export() != int64(42) {
		t.Errorf("after cancellation: %#v, %v", got, err)
	}
}

func TestRunDeadlineAppliesToCalls(t *testing.T) {
	// TODO: Run's context does not bound a call given a different one, because
	// Signaller.Context watches the deadline rather than adopting it, so an
	// expiry arrives as context.Canceled. Fix by having Run return ctx.Err()
	// when the borrow's context ended.
	//
	// TestRunContextBoundsTheCallback asserts the current behaviour and
	// contradicts this test; reconcile the two when unskipping.
	t.Skip("Run reports Canceled, not DeadlineExceeded, for an unrelated call context")
	p := spinner(t)

	callCtx, cancelCall := context.WithCancel(context.Background())
	defer cancelCall()

	watchdog := time.AfterFunc(3*time.Second, cancelCall)
	defer watchdog.Stop()

	runCtx, cancelRun := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelRun()

	called := false
	err := p.Run(runCtx, func(in *BorrowedInstance) error {
		called = true
		_, err := in.Call(callCtx, "spin")

		return err
	})

	if !called {
		t.Fatal("Run did not invoke the callback")
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run = %v, want DeadlineExceeded", err)
	}

	// Cancellation belongs to the finished run, not the next pool borrower.
	if got, err := progCall(callCtx, p, "double", 21); err != nil || got.Export() != int64(42) {
		t.Errorf("after cancellation: %v, %v", got, err)
	}
}

func TestRunAllowsShorterCallDeadline(t *testing.T) {
	p := spinner(t)

	runCtx, cancelRun := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRun()

	err := p.Run(runCtx, func(in *BorrowedInstance) error {
		callCtx, cancelCall := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancelCall()

		if _, err := in.Call(callCtx, "spin"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Call = %v, want DeadlineExceeded", err)
		}

		if err := runCtx.Err(); err != nil {
			t.Fatalf("call did not stop before the run deadline: %v", err)
		}

		got, err := in.Call(runCtx, "double", 21)
		if err != nil || got.Export() != int64(42) {
			t.Errorf("after call deadline: %v, %v", got, err)
		}

		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsOperationsAfterDeadline(t *testing.T) {
	// TODO: needs an expired flag beside closed, set when the borrow's context
	// ends. The honest answer is probably that the borrow ended rather than
	// DeadlineExceeded, since the deadline is a fact about the borrow and not
	// about the call just attempted, so this test likely changes shape.
	t.Skip("a BorrowedInstance does not refuse operations after the borrow's context ends")
	p := spinner(t)

	runCtx, cancelRun := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelRun()

	called := false

	err := p.Run(runCtx, func(in *BorrowedInstance) error {
		called = true

		<-runCtx.Done()

		ctx := context.Background()
		for name, err := range map[string]error{
			"Call": second(in.Call(ctx, "double", 21)),
			"Eval": second(in.Eval(ctx, "1")),
			"Get":  second(in.Get(ctx, "double")),
			"Set":  in.Set(ctx, "x", 1),
			"Exec": in.Exec(ctx, "x = 1"),
		} {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("%s = %v, want DeadlineExceeded", name, err)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if !called {
		t.Fatal("Run did not invoke the callback")
	}
}

func TestRunCancellationReachesHostFunction(t *testing.T) {
	// TODO: dispatcher.go builds the host function's context from
	// context.Background, so the shutdown signal carries cancellation but the
	// caller's values and deadline never reach it. Predates Program.Run and
	// affects a plain Instance too. Fixing it needs the operation's context
	// reachable from Module, which is a context in a struct.
	t.Skip("a host function does not see the caller's context values")

	type contextKey struct{}

	runCtx, cancelRun := context.WithCancelCause(context.Background())
	defer cancelRun(nil)

	callCtx, cancelCall := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelCall()

	callCtx = context.WithValue(callCtx, contextKey{}, "operation")

	p, err := NewProgram(t.Context(), WithHostFunc("wait", func(ctx context.Context, args []Value) (Value, error) {
		if got := ctx.Value(contextKey{}); got != "operation" {
			t.Errorf("context value = %v, want operation", got)
		}

		cancelRun(errors.New("caller stopped the run"))
		<-ctx.Done()

		return None(), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	err = p.Run(runCtx, func(in *BorrowedInstance) error {
		_, err := in.Call(callCtx, "wait")
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want Canceled", err)
	}

	if err := callCtx.Err(); err != nil {
		t.Fatalf("host function waited for the operation deadline: %v", err)
	}
}

func TestRunContextDeadlineVisibleToHostFunction(t *testing.T) {
	// TODO: same cause as TestRunCancellationReachesHostFunction.
	t.Skip("a host function does not see the caller's context deadline or values")

	type contextKey struct{}

	runCtx, cancelRun := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelRun()

	deadline, _ := runCtx.Deadline()
	callCtx := context.WithValue(context.Background(), contextKey{}, "operation")

	p, err := NewProgram(t.Context(), WithHostFunc("check", func(ctx context.Context, args []Value) (Value, error) {
		if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) {
			t.Errorf("context deadline = %v, %v; want %v", got, ok, deadline)
		}

		if got := ctx.Value(contextKey{}); got != "operation" {
			t.Errorf("context value = %v, want operation", got)
		}

		return None(), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.Run(runCtx, func(in *BorrowedInstance) error {
		_, err := in.Call(callCtx, "check")
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCancelBeforeCall(t *testing.T) {
	p := spinner(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := progCall(ctx, p, "spin"); !errors.Is(err, context.Canceled) {
		t.Fatalf("spin() = %v, want Canceled", err)
	}

	if got, err := progCall(context.Background(), p, "double", int64(1)); err != nil || got.Export() != int64(2) {
		t.Errorf("after a pre-cancelled call: %#v, %v", got, err)
	}
}

func TestCancelIsNotSwallowedByBareExcept(t *testing.T) {
	p := spinner(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)

	go func() {
		_, err := progCall(ctx, p, "spin_swallowing")
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
	p, err := NewProgram(context.Background(), WithSource(`
def work(n):
    total = 0
    for i in range(n):
        total += i
    return total

def spin():
    while True:
        pass
`))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	long := make(chan error, 1)

	go func() {
		_, err := progCall(context.Background(), p, "work", int64(2_000_000))
		long <- err
	}()

	// Give the long call time to be in flight, then cancel a different one.
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if _, err := progCall(ctx, p, "spin"); !errors.Is(err, context.DeadlineExceeded) {
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

	usd := func(_ context.Context, args []Value) (Value, error) {
		calls.Add(1)

		code, err := args[0].AsString()
		if err != nil {
			return Value{}, err
		}

		rate, ok := rates[code]
		if !ok {
			return Value{}, Raise("KeyError", code)
		}

		amount, err := args[1].AsInt()
		if err != nil {
			return Value{}, err
		}

		return Float(rate * float64(amount)), nil
	}

	// The pool holds one idle instance, so concurrent calls have to restore
	// fresh ones from the snapshot. That is the path the binding must survive.
	p, err := NewProgram(ctx, WithSource(`
def convert(code, amount):
    return round(usd(code, amount), 2)
`), WithHostFunc("usd", usd), WithMaxIdle(5))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	got, err := progCall(ctx, p, "convert", "EUR", 100)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}

	if got.Export() != 109.0 {
		t.Fatalf("got %v, want 109.0", got)
	}

	const n = 16

	var wg sync.WaitGroup

	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			v, err := progCall(ctx, p, "convert", "GBP", 10)
			if err != nil {
				errs[i] = err
				return
			}

			if v.Export() != 12.7 {
				errs[i] = fmt.Errorf("got %v, want 12.7", v)
			}
		})
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

	p, err := NewProgram(ctx, WithSource(`
LIMIT = fetch_limit()

def within(n):
    return n <= LIMIT
`), WithHostFunc("fetch_limit", func(context.Context, []Value) (Value, error) {
		calls.Add(1)
		return Int(5), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	for _, tc := range []struct {
		n    int
		want bool
	}{{3, true}, {5, true}, {6, false}} {
		got, err := progCall(ctx, p, "within", tc.n)
		if err != nil {
			t.Fatalf("within(%d): %v", tc.n, err)
		}

		if got.Export() != tc.want {
			t.Errorf("within(%d) = %v, want %v", tc.n, got, tc.want)
		}
	}

	if calls.Load() != 1 {
		t.Errorf("fetch_limit ran %d times, want 1 (it should be in the snapshot)", calls.Load())
	}
}

func TestProgramHostFuncError(t *testing.T) {
	ctx := context.Background()

	p, err := NewProgram(ctx, WithSource(`
def lookup(code):
    try:
        return rate(code)
    except KeyError:
        return None
`), WithHostFunc("rate", func(ctx context.Context, args []Value) (Value, error) {
		str, _ := args[0].AsString()
		return Value{}, Raise("KeyError", str)
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	got, err := progCall(ctx, p, "lookup", "ZAR")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	if got.Export() != nil {
		t.Fatalf("got %v, want nil", got)
	}

	if _, err := progCall(ctx, p, "rate", "ZAR"); err == nil {
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

	if _, err := progCall(ctx, p, "lookup", "ZAR"); err != nil {
		t.Fatalf("after error: %v", err)
	}
}

func TestProgramStdout(t *testing.T) {
	spins := 3
	spinFn := func(context.Context, []Value) (Value, error) {
		if spins == 0 {
			return Bool(false), nil
		}

		spins--

		return Bool(true), nil
	}

	out := new(bytes.Buffer)

	p, err := NewProgram(t.Context(), WithSource(`
def handle():
	while spin():
		print("duck")
	print("goose")
`), WithHostFunc("spin", spinFn), WithStdout(out))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	if _, err := progCall(t.Context(), p, "handle"); err != nil {
		t.Fatalf("call: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")

	want := []string{"duck", "duck", "duck", "goose"}
	if !slices.Equal(lines, want) {
		t.Errorf("got %v, want %v", lines, want)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.String()
}

func TestProgramStdoutConcurrent(t *testing.T) {
	const workers = 8

	out := new(syncBuffer)

	p, err := NewProgram(t.Context(), WithSource("def handle(n):\n    print('n', n)\n"), WithStdout(out), WithMaxIdle(workers))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	defer p.Close()

	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			if _, err := progCall(t.Context(), p, "handle", i); err != nil {
				t.Errorf("call %d: %v", i, err)
			}
		})
	}

	wg.Wait()

	// A synchronised sink loses no output, but does not keep a line together:
	// MicroPython emits one print() as several writes, so concurrent calls
	// interleave within a line.
	got := out.String()
	if n := strings.Count(got, "\n"); n != workers {
		t.Errorf("got %d newlines, want %d:\n%s", n, workers, got)
	}

	for i := range workers {
		if want := fmt.Sprint(i); !strings.Contains(got, want) {
			t.Errorf("output for call %d is missing entirely:\n%s", i, got)
		}
	}
}

// progCall runs one function call on a pooled interpreter, the shape
// Program.Call used to have. Results carrying guest handles do not outlive the
// run, so every caller here asks for plain data.
func progCall(ctx context.Context, p *Program, name string, args ...any) (Value, error) {
	var out Value

	err := p.Run(ctx, func(in *BorrowedInstance) error {
		v, err := in.Call(ctx, name, args...)
		if err != nil {
			return err
		}

		out = v

		return nil
	})

	return out, err
}

// A BorrowedInstance is valid only for the life of the callback. The interpreter
// goes back to the pool when Run returns, so anything the callback left running
// has to fail rather than reach an interpreter that by then belongs to another
// run.
func TestBorrowedInstanceIsInvalidAfterRun(t *testing.T) {
	p, err := NewProgram(t.Context(), WithSource("def f():\n    return 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	var escaped *BorrowedInstance

	err = p.Run(t.Context(), func(in *BorrowedInstance) error {
		escaped = in
		val, err := in.Call(t.Context(), "f")

		fmt.Printf("val: %v\n", val)

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	for name, err := range map[string]error{
		"Call": second(escaped.Call(ctx, "f")),
		"Eval": second(escaped.Eval(ctx, "1")),
		"Get":  second(escaped.Get(ctx, "f")),
		"Exec": escaped.Exec(ctx, "x = 1"),
		"Set":  escaped.Set(ctx, "x", 1),
	} {
		if !errors.Is(err, ErrRunReturned) {
			t.Errorf("%s after Run = %v, want ErrRunReturned", name, err)
		}
	}
}

func second(_ Value, err error) error { return err }

// A cancelled caller is reported rather than silently borrowing an interpreter,
// and a nil callback is a mistake rather than a panic.
func TestRunRejectsBadInput(t *testing.T) {
	p, err := NewProgram(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if err := p.Run(t.Context(), nil); err == nil {
		t.Error("Run accepted a nil callback")
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	ran := false

	if err := p.Run(cancelled, func(*BorrowedInstance) error {
		ran = true
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Errorf("Run on a cancelled context = %v, want context.Canceled", err)
	}

	if ran {
		t.Error("Run invoked the callback despite a cancelled context")
	}
}

func TestRunPreservesCallbackError(t *testing.T) {
	p := newProgram(t)
	want := errors.New("callback failed")

	err := p.Run(t.Context(), func(*BorrowedInstance) error {
		return want
	})
	if err != want {
		t.Fatalf("Run = %v, want the original callback error", err)
	}
}

// The hazard a borrow has to survive: a goroutine the callback started, still
// holding the instance, running after Run returned it to the pool and another
// run picked it up. It has to be refused rather than interleave with that run.
func TestBorrowedInstanceOutlivingRun(t *testing.T) {
	p, err := NewProgram(t.Context(), WithSource("def f(n):\n    return n\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	var (
		release = make(chan struct{})
		leaked  = make(chan error, 1)
	)

	if err := p.Run(t.Context(), func(in *BorrowedInstance) error {
		go func() {
			<-release

			_, err := in.Call(context.Background(), "f", int64(1))
			leaked <- err
		}()

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// A second borrow, most likely of the same interpreter, is in progress when
	// the leaked goroutine wakes.
	if err := p.Run(t.Context(), func(in *BorrowedInstance) error {
		close(release)

		if err := <-leaked; !errors.Is(err, ErrRunReturned) {
			t.Errorf("the leaked goroutine got %v, want ErrRunReturned", err)
		}

		got, err := in.Call(context.Background(), "f", int64(2))
		if err != nil {
			return err
		}

		if n, _ := got.AsInt(); n != 2 {
			t.Errorf("the second borrow saw %v, want 2", n)
		}

		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Run's context stops the callback's work even when the callback hands its
// calls a different one: the callback no longer receives a context, so without
// this a runaway loop given context.Background would never stop.
//
// Which error surfaces depends on which context ended. A call given Run's own
// context sees it expire. One given an unrelated context never does; the guest
// is interrupted instead, so the interrupt is what the caller sees.
// TestRunDeadlineAppliesToCalls wants the second case to report the deadline
// too, which is a change to Run rather than to the interruption.
func TestRunContextStopsTheCallback(t *testing.T) {
	p, err := NewProgram(t.Context(), WithSource("def spin():\n    while True:\n        pass\n"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	calls := map[string]func(*BorrowedInstance, context.Context) error{
		"Call": func(in *BorrowedInstance, ctx context.Context) error {
			_, err := in.Call(ctx, "spin")
			return err
		},
		"Eval": func(in *BorrowedInstance, ctx context.Context) error {
			_, err := in.Eval(ctx, "spin()")
			return err
		},
		"Exec": func(in *BorrowedInstance, ctx context.Context) error {
			return in.Exec(ctx, "spin()")
		},
	}

	for name, call := range calls {
		t.Run(name+"/run", func(t *testing.T) {
			deadline, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()

			err := p.Run(deadline, func(in *BorrowedInstance) error {
				return call(in, deadline)
			})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Run = %v, want DeadlineExceeded", err)
			}
		})

		t.Run(name+"/unrelated", func(t *testing.T) {
			deadline, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()

			err := p.Run(deadline, func(in *BorrowedInstance) error {
				return call(in, context.Background())
			})

			var exc *PythonError
			if !errors.As(err, &exc) || exc.Type() != "KeyboardInterrupt" {
				t.Fatalf("Run = %v, want the guest to be interrupted", err)
			}
		})
	}
}
