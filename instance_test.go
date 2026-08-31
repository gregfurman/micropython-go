package micropython

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gregfurman/micropython-go/internal/value"
)

const catchAs = `
ok = False
try:
    f()
except %s:
    ok = True
`

func newT(t *testing.T) *Instance {
	t.Helper()
	in, err := NewInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close() })
	return in
}

func TestExecThenCall(t *testing.T) {
	ctx := context.Background()
	in := newT(t)

	if err := in.Exec(ctx, `
def double(n):
    return n * 2

def shout(s):
    return s.upper()
`); err != nil {
		t.Fatal(err)
	}

	if got, err := in.Call(ctx, "double", Of(21)); err != nil || got != int64(42) {
		t.Errorf("double(21) = %#v, %v", got, err)
	}
	if got, err := in.Call(ctx, "shout", "hi"); err != nil || got != "HI" {
		t.Errorf("shout(\"hi\") = %#v, %v", got, err)
	}
}

func TestExecOutput(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	in, err := NewInstance(ctx, WithStdout(&out))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close() })

	if err := in.Exec(ctx, "print('hello')\n"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "hello\n" {
		t.Errorf("out = %q", got)
	}
}

func TestCallByName(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	if err := in.Exec(ctx, "def add(a, b):\n    return a + b\n"); err != nil {
		t.Fatal(err)
	}
	got, err := in.Call(ctx, "add", Of(20), Of(22))
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(42) {
		t.Errorf("add(20, 22) = %#v, want 42", got)
	}
}

func TestClose(t *testing.T) {
	ctx := context.Background()
	in, err := NewInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Call(ctx, "print"); err == nil {
		t.Error("expected an error after Close")
	}
	if err := in.Exec(ctx, "pass"); err == nil {
		t.Error("expected an error after Close")
	}
}

func TestPythonError(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	if err := in.Exec(ctx, "1/0\n"); err == nil {
		t.Fatal("expected a Python error")
	}
	// The instance must still work afterwards.
	if got, err := in.Call(ctx, "len", "abc"); err != nil || got != int64(3) {
		t.Errorf("after error: %#v, %v", got, err)
	}
}

func TestEval(t *testing.T) {
	got, err := newT(t).Eval(t.Context(), "[1, 2, 3]")
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{int64(1), int64(2), int64(3)}; !reflect.DeepEqual(got, want) {
		t.Errorf("Eval = %#v, want %#v", got, want)
	}
}

func TestEvalImport(t *testing.T) {
	got, err := newT(t).Eval(t.Context(), "__import__('math').__dict__.get('__name__')")
	if err != nil {
		t.Fatal(err)
	}
	if want := "math"; got != want {
		t.Errorf("Eval = %#v, want %#v", got, want)
	}
}

func TestGlobals(t *testing.T) {
	p, err := CompileSource(context.Background(), `
def run():
    return [
        NAME,
        LIMITS["retries"],
        sorted(TAGS),
        COUNTS,
        FLAG,
        RATIO,
        RAW,
        NOTHING,
        sorted(UNIQUE),
    ]
`,
		WithGlobals(Globals{
			"NAME":    Str("service"),
			"LIMITS":  Dict(Item{Key: Str("retries"), Val: Int(3)}),
			"TAGS":    Strs("b", "a"),
			"COUNTS":  Tuple(Int(1), Int(2)),
			"FLAG":    Bool(true),
			"RATIO":   Float(0.5),
			"RAW":     Bytes([]byte("hi")),
			"NOTHING": None(),
			"UNIQUE":  Set(Int(2), Int(1), Int(2)),
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	got, err := p.Call(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	want := []any{
		"service", int64(3), []any{"a", "b"},
		value.Tuple{int64(1), int64(2)}, true, 0.5, []byte("hi"), nil,
		[]any{int64(1), int64(2)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("run() =\n\t%#v\nwant\n\t%#v", got, want)
	}
}

func TestValueLiftMatchesRoundTrip(t *testing.T) {
	values := []Value{
		None(), Bool(true), Int(-7), Float(1.5), Str("x"), Bytes([]byte("ab")),
		List(Int(1), Str("a")),
		Tuple(Int(1)),
		Dict(Item{Key: Str("k"), Val: Int(1)}),
	}

	p, err := CompileSource(context.Background(), "def echo(v):\n    return v\n")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	for _, v := range values {
		t.Run(v.Type(), func(t *testing.T) {
			in, err := NewInstance(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()

			if err := in.Set(t.Context(), "V", v); err != nil {
				t.Fatal(err)
			}
			got, err := in.Eval(t.Context(), "V")
			if err != nil {
				t.Fatal(err)
			}
			if want := v.lift(); !reflect.DeepEqual(got, want) {
				t.Errorf("guest sent %#v, Lift says %#v", got, want)
			}
		})
	}
}

func TestPythonValuesPassedBack(t *testing.T) {
	in := newT(t)

	got, err := in.Eval(t.Context(), "lambda: 1")
	if err != nil {
		t.Fatal(err)
	}
	// NOTE: we don't export this for now.
	obj, ok := got.(value.Object)
	if !ok {
		t.Fatalf("got %#v (%T), want an Object", got, got)
	}

	if err := in.Exec(t.Context(), "def f(v):\n    return v\n"); err != nil {
		t.Fatal(err)
	}
	_, err = in.Call(t.Context(), "f", obj)
	if err == nil {
		t.Fatal("an Object was accepted as an argument")
	}
	if !strings.Contains(err.Error(), "cannot be passed back") {
		t.Errorf("Call = %v, want it to say the Object cannot be passed back", err)
	}

	// An exception, by contrast, is fully described by its type and message,
	// so it does go back -- as a real exception, not a dict of its fields.
	_, callErr := in.Call(t.Context(), "f")
	var exc *PythonError
	if !errors.As(callErr, &exc) {
		t.Fatalf("got %v (%T), want *Exception", callErr, callErr)
	}
	if err := in.Exec(t.Context(), "def kind(e):\n    return type(e).__name__\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := in.Call(t.Context(), "kind", exc); err != nil || got != exc.Type() {
		t.Errorf("passing an Exception back = %#v, %v; want %q", got, err, exc.Type())
	}

	// And the instance is unharmed.
	if got, err := in.Call(t.Context(), "f", int64(1)); err != nil || got != int64(1) {
		t.Errorf("after the refusals: %#v, %v", got, err)
	}
}

func TestExceptionLowers(t *testing.T) {
	p, err := CompileSource(context.Background(), `
def raise_it():
    raise BAD

def caught():
    try:
        raise BAD
    except ValueError as e:
        return "caught:" + str(e)

def unknown():
    try:
        raise ODD
    except RuntimeError as e:
        return "fallback:" + str(e)
`, WithGlobals(Globals{
		"BAD": Exception("ValueError", "bad input"),
		// A type the guest has never heard of falls back to HostError, which
		// subclasses RuntimeError, rather than failing twice on the way to
		// reporting the first failure.
		"ODD": Exception("NoSuchError", "still readable"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if got, err := p.Call(t.Context(), "caught"); err != nil || got != "caught:bad input" {
		t.Errorf("caught() = %#v, %v", got, err)
	}
	if got, err := p.Call(t.Context(), "unknown"); err != nil || got != "fallback:still readable" {
		t.Errorf("unknown() = %#v, %v", got, err)
	}

	// Raised and uncaught, it comes back out as the same exception.
	var exc *PythonError
	if _, err := p.Call(t.Context(), "raise_it"); !errors.As(err, &exc) {
		t.Fatalf("got %v (%T), want *Exception", err, err)
	} else if exc.Type() != "ValueError" || exc.Message() != "bad input" {
		t.Errorf("round trip = %q / %q", exc.Type(), exc.Message())
	}
}

func TestBuiltValueAsCallArgument(t *testing.T) {
	p, err := CompileSource(context.Background(), "def echo(v):\n    return v\n")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	tests := []struct {
		arg  Value
		want any
	}{
		{Int(3), int64(3)},
		{Str("x"), "x"},
		{None(), nil},
		{List(Int(1), Int(2)), []any{int64(1), int64(2)}},
		{Dict(Item{Key: Str("k"), Val: Int(1)}), map[string]any{"k": int64(1)}},
	}

	for _, tt := range tests {
		t.Run(tt.arg.Type(), func(t *testing.T) {
			got, err := p.Call(t.Context(), "echo", tt.arg)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("echo(%s) = %#v, want %#v", tt.arg.Type(), got, tt.want)
			}
		})
	}
}

func TestInstanceCancel(t *testing.T) {
	in := newT(t)
	if err := in.Exec(t.Context(), spinSrc); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := in.Call(context.Background(), "spin")
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if err := in.Cancel(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("spin() returned without an error after Cancel")
		}
		if !errors.Is(err, ErrInterrupted) {
			t.Logf("cancelled call reported: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Cancel did not stop the guest")
	}

	// Cancel is a request for one call, not a permanent state.
	if got, err := in.Call(context.Background(), "double", int64(4)); err != nil || got != int64(8) {
		t.Errorf("after Cancel: %#v, %v", got, err)
	}
}

func TestWithHeapSize(t *testing.T) {
	src := "def big(n):\n    return len(bytearray(n))\n"

	small, err := CompileSource(context.Background(), src, WithHeapSize(128*1024))
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()

	// Comfortably inside a 128KB heap.
	if got, err := small.Call(t.Context(), "big", 16*1024); err != nil || got != int64(16*1024) {
		t.Errorf("16KB in a 128KB heap: %#v, %v", got, err)
	}

	// Beyond it, and the guest says so rather than the module dying.
	var exc *PythonError
	if _, err := small.Call(t.Context(), "big", 4*1024*1024); !errors.As(err, &exc) {
		t.Fatalf("4MB in a 128KB heap: %v, want an *Exception", err)
	} else if exc.Type() != "MemoryError" {
		t.Errorf("Type = %q, want MemoryError", exc.Type())
	}

	// The Program is unharmed, and a larger heap takes what the smaller could not.
	if got, err := small.Call(t.Context(), "big", 16*1024); err != nil || got != int64(16*1024) {
		t.Errorf("after MemoryError: %#v, %v", got, err)
	}

	big, err := CompileSource(context.Background(), src, WithHeapSize(4*1024*1024))
	if err != nil {
		t.Fatal(err)
	}
	defer big.Close()
	if got, err := big.Call(t.Context(), "big", 2*1024*1024); err != nil || got != int64(2*1024*1024) {
		t.Errorf("2MB in a 4MB heap: %#v, %v", got, err)
	}
}

func TestDefineFunction(t *testing.T) {
	ctx := context.Background()
	in := newT(t)

	if err := in.DefineFunction(ctx, "shout", func(args []any) (any, error) {
		s, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("want str, got %T", args[0])
		}
		return strings.ToUpper(s), nil
	}); err != nil {
		t.Fatal(err)
	}

	// Callable from an expression, from a script, and as a value passed around.
	if got, err := in.Eval(ctx, `shout("hi")`); err != nil || got != "HI" {
		t.Errorf("shout(\"hi\") = %#v, %v", got, err)
	}
	if err := in.Exec(ctx, `
def twice(s):
    return shout(s) + shout(s)
`); err != nil {
		t.Fatal(err)
	}
	if got, err := in.Call(ctx, "twice", "ab"); err != nil || got != "ABAB" {
		t.Errorf("twice(\"ab\") = %#v, %v", got, err)
	}
}

func TestDefineFunctionArgumentsAndResults(t *testing.T) {
	ctx := context.Background()
	in := newT(t)

	if err := in.DefineFunction(ctx, "echo", func(args []any) (any, error) {
		return args[0], nil
	}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		expr string
		want any
	}{
		{`echo("s")`, "s"},
		{`echo(7)`, int64(7)},
		{`echo(1.5)`, 1.5},
		{`echo(True)`, true},
		{`echo(None) is None`, true},
		{`echo(b"xy")`, []byte("xy")},
		{`echo([1, "a"])`, []any{int64(1), "a"}},
		{`echo({"k": 1}) == {"k": 1}`, true},
	}
	for _, tc := range tests {
		got, err := in.Eval(ctx, tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s = %#v, want %#v", tc.expr, got, tc.want)
		}
	}
}

func TestDefineFunctionErrors(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		fn       HostFunc
		wantType string
		wantMsg  string
	}{
		{"plain error", func([]any) (any, error) { return nil, errors.New("boom") }, "HostError", "boom"},
		{"Raise names a builtin", func([]any) (any, error) { return nil, Raise("KeyError", "missing") }, "KeyError", "missing"},
		{"Raise with unknown class", func([]any) (any, error) { return nil, Raise("Nope", "x") }, "HostError", "x"},
		{"panic", func([]any) (any, error) { panic("kaboom") }, "HostError", "host function panicked"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := newT(t)
			if err := in.DefineFunction(ctx, "f", tc.fn); err != nil {
				t.Fatal(err)
			}

			_, err := in.Eval(ctx, `f()`)
			var exc *PythonError
			if !errors.As(err, &exc) {
				t.Fatalf("err = %v (%T), want *PythonError", err, err)
			}
			if exc.Type() != tc.wantType {
				t.Errorf("Type = %q, want %q", exc.Type(), tc.wantType)
			}
			if !strings.Contains(exc.Message(), tc.wantMsg) {
				t.Errorf("Message = %q, want it to contain %q", exc.Message(), tc.wantMsg)
			}

			// Guest code can catch it, and the instance survives.
			if err := in.Exec(ctx, fmt.Sprintf(catchAs, tc.wantType)); err != nil {
				t.Fatalf("guest could not catch %s: %v", tc.wantType, err)
			}
			if got, err := in.Eval(ctx, "ok"); err != nil || got != true {
				t.Errorf("except %s did not fire: %#v, %v", tc.wantType, got, err)
			}
		})
	}
}

func TestDefineFunctionNil(t *testing.T) {
	if err := newT(t).DefineFunction(context.Background(), "f", nil); err == nil {
		t.Fatal("DefineFunction(nil) = nil, want error")
	}
}

func TestDefineFunctionSurvivesClone(t *testing.T) {
	ctx := context.Background()
	in := newT(t)

	calls := 0
	if err := in.DefineFunction(ctx, "tick", func([]any) (any, error) {
		calls++
		return int64(calls), nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Eval(ctx, "tick()"); err != nil {
		t.Fatal(err)
	}

	clone, err := in.Clone(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Close()

	// The clone reaches the same Go closure, so the counter keeps advancing.
	if got, err := clone.Eval(ctx, "tick()"); err != nil || got != int64(2) {
		t.Errorf("tick() on clone = %#v, %v; want 2", got, err)
	}
}

func TestHostErrorHierarchy(t *testing.T) {
	ctx := context.Background()
	in := newT(t)

	if err := in.DefineFunction(ctx, "f", func([]any) (any, error) {
		return nil, errors.New("boom")
	}); err != nil {
		t.Fatal(err)
	}

	// NOTE: HostErrors are subclasses of RuntimeError
	for _, class := range []string{"HostError", "RuntimeError", "Exception"} {
		if err := in.Exec(ctx, fmt.Sprintf(catchAs, class)); err != nil {
			t.Fatalf("except %s: %v", class, err)
		}
		if got, err := in.Eval(ctx, "ok"); err != nil || got != true {
			t.Errorf("except %s did not catch HostError: %#v, %v", class, got, err)
		}
	}

	// It is still narrower than RuntimeError: a plain RuntimeError is not one.
	if got, err := in.Eval(ctx, "isinstance(RuntimeError('x'), HostError)"); err != nil || got != false {
		t.Errorf("RuntimeError is not a HostError: %#v, %v", got, err)
	}
}
