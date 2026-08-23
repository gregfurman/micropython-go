package micropython

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Go functions called from Python.
//
// Every one has the same signature and unpacks its own arguments, which is
// what lets one shape serve every arity and keyword arguments alike.

func TestFuncCall(t *testing.T) {
	p, err := Compile(context.Background(), `
def hypot(a, b):
    return sqrt(pow(x=a, y=2) + pow(b, 2))

def greet(who):
    return upper("hello " + who)
`, Declare{
		"pow": Fn(func(c *Call) (any, error) {
			var x, y float64
			if err := c.Unpack("x", &x, "y", &y); err != nil {
				return nil, err
			}
			return math.Pow(x, y), nil
		}),
		"sqrt": Fn(func(c *Call) (any, error) {
			var x float64
			if err := c.Unpack("x", &x); err != nil {
				return nil, err
			}
			return math.Sqrt(x), nil
		}),
		"upper": Fn(func(c *Call) (any, error) {
			var s string
			if err := c.Unpack("s", &s); err != nil {
				return nil, err
			}
			return strings.ToUpper(s), nil
		}),
	}.Option())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// hypot passes one argument by keyword and one positionally.
	if got, err := p.Call(t.Context(), "hypot", 3, 4); err != nil || got != 5.0 {
		t.Errorf("hypot(3, 4) = %#v, %v", got, err)
	}
	if got, err := p.Call(t.Context(), "greet", "world"); err != nil || got != "HELLO WORLD" {
		t.Errorf("greet() = %#v, %v", got, err)
	}
}

// Unpack is the whole binding, so what it accepts and refuses is the contract.
func TestFuncUnpack(t *testing.T) {
	p, err := Compile(context.Background(), `
def run(expr):
    try:
        return eval(expr)
    except Exception as e:
        return "!" + str(e)
`, Declare{
		// n is required; scale is optional and keeps its default when absent.
		"scaled": Fn(func(c *Call) (any, error) {
			var n int
			scale := 10
			if err := c.Unpack("n", &n, "scale?", &scale); err != nil {
				return nil, err
			}
			return n * scale, nil
		}),
		"widen": Fn(func(c *Call) (any, error) {
			var f float64
			if err := c.Unpack("f", &f); err != nil {
				return nil, err
			}
			return f / 2, nil
		}),
		"count": Fn(func(c *Call) (any, error) {
			var items []any
			if err := c.Unpack("items", &items); err != nil {
				return nil, err
			}
			return len(items), nil
		}),
		"narrow": Fn(func(c *Call) (any, error) {
			var n int8
			if err := c.Unpack("n", &n); err != nil {
				return nil, err
			}
			return int64(n), nil
		}),
	}.Option())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	tests := []struct {
		expr string
		want any
	}{
		{`scaled(3)`, int64(30)},            // the default is kept
		{`scaled(3, 2)`, int64(6)},          // positional
		{`scaled(3, scale=4)`, int64(12)},   // by keyword
		{`scaled(n=3, scale=4)`, int64(12)}, // both by keyword
		{`widen(5)`, 2.5},                   // an int widens to a float
		{`count((1, 2, 3))`, int64(3)},      // a tuple reads as a sequence
		{`count({1, 2})`, int64(2)},         // and so does a set
		{`narrow(127)`, int64(127)},
		{`scaled()`, `!scaled() missing argument "n"`},
		{`scaled(1, 2, 3)`, `!scaled() takes at most 2 arguments (3 given)`},
		{`scaled(1, nope=2)`, `!scaled() got an unexpected keyword argument "nope"`},
		{`scaled(1, n=2)`, `!scaled() got multiple values for argument "n"`},
		{`widen("a")`, `!widen() argument "f": cannot use str as *float64`},
		{`narrow(999)`, `!narrow() argument "n": 999 does not fit in int8`},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := p.Call(t.Context(), "run", tt.expr)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("%s = %#v, want %#v", tt.expr, got, tt.want)
			}
		})
	}
}

// A Go error becomes an exception the guest can catch, and an *Exception picks
// which one.
func TestFuncError(t *testing.T) {
	p, err := Compile(context.Background(), `
def caught(kind):
    try:
        fail(kind)
    except KeyError as e:
        return "KeyError:" + str(e)
    except RuntimeError as e:
        return "RuntimeError:" + str(e)

def uncaught():
    return fail("plain")
`, WithFunc("fail", func(c *Call) (any, error) {
		var kind string
		if err := c.Unpack("kind", &kind); err != nil {
			return nil, err
		}
		if kind == "typed" {
			return nil, &Exception{Type: "KeyError", Message: "missing"}
		}
		return nil, errors.New("something went wrong")
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if got, err := p.Call(t.Context(), "caught", "typed"); err != nil || got != "KeyError:missing" {
		t.Errorf("typed error = %#v, %v", got, err)
	}
	if got, err := p.Call(t.Context(), "caught", "plain"); err != nil || got != "RuntimeError:something went wrong" {
		t.Errorf("plain error = %#v, %v", got, err)
	}

	var exc *Exception
	if _, err := p.Call(t.Context(), "uncaught"); !errors.As(err, &exc) {
		t.Fatalf("got %v (%T), want *Exception", err, err)
	} else if exc.Type != "RuntimeError" || exc.Message != "something went wrong" {
		t.Errorf("uncaught = %q / %q", exc.Type, exc.Message)
	}
}

// A panic in Go code is the guest's failure, not the interpreter's.
func TestFuncPanic(t *testing.T) {
	p, err := Compile(context.Background(), `
def run():
    try:
        boom()
    except Exception as e:
        return str(e)
    return "no exception"

def fine():
    return 1
`, WithFunc("boom", func(c *Call) (any, error) { panic("oh no") }))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	got, err := p.Call(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := got.(string); !strings.Contains(s, "oh no") {
		t.Errorf("run() = %#v, want the panic's message", got)
	}
	if got, err := p.Call(t.Context(), "fine"); err != nil || got != int64(1) {
		t.Errorf("after a panic: %#v, %v", got, err)
	}
}

// A host function has to survive the pool: the ids live in the snapshot, so
// every interpreter restored from it must resolve them to the same functions.
func TestFuncAcrossPool(t *testing.T) {
	var calls atomic.Int64

	p, err := Compile(context.Background(), `
def run(n):
    return double(n)
`, WithFunc("double", func(c *Call) (any, error) {
		var n int64
		if err := c.Unpack("n", &n); err != nil {
			return nil, err
		}
		calls.Add(1)
		return n * 2, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	const goroutines, each = 8, 20

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*each)
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				n := int64(g*each + i)
				got, err := p.Call(t.Context(), "run", n)
				if err != nil {
					errs <- err
					return
				}
				if got != 2*n {
					errs <- fmt.Errorf("double(%d) = %v", n, got)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	if got := calls.Load(); got != goroutines*each {
		t.Errorf("the host function ran %d times, want %d", got, goroutines*each)
	}
}

// A host function reached from inside a value the interpreter is already
// streaming back: __repr__ runs during the walk, so the arguments arrive
// halfway through the result's own decode. This is what the saved decoder in
// Xcall_begin exists for.
func TestFuncDuringEmit(t *testing.T) {
	p, err := Compile(context.Background(), `
class Tagged:
    def __repr__(self):
        return tag("x")

def run():
    return [1, Tagged(), 3]
`, WithFunc("tag", func(c *Call) (any, error) {
		var s string
		if err := c.Unpack("s", &s); err != nil {
			return nil, err
		}
		return "<" + s + ">", nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	got, err := p.Call(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	items, ok := got.([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("run() = %#v, want a 3-element list", got)
	}
	if items[0] != int64(1) || items[2] != int64(3) {
		t.Errorf("run() = %#v -- the outer decode was disturbed", got)
	}
}

// Values bound from the host, without going through source text. A Declare
// holds functions and plain values together.
func TestGlobals(t *testing.T) {
	p, err := Compile(context.Background(), `
def run():
    return [NAME, LIMITS["retries"], sorted(TAGS)]
`, Declare{
		"NAME":   "service",
		"LIMITS": map[string]any{"retries": 3},
		"TAGS":   []string{"b", "a"},
	}.Option())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	got, err := p.Call(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"service", int64(3), []any{"a", "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("run() = %#v, want %#v", got, want)
	}
}

func BenchmarkFuncCall(b *testing.B) {
	p, err := Compile(context.Background(), `
def once():
    return f(1, 2)

def many():
    total = 0
    for i in range(100):
        total += f(i, 1)
    return total
`, WithFunc("f", func(c *Call) (any, error) {
		var x, y int64
		if err := c.Unpack("x", &x, "y", &y); err != nil {
			return nil, err
		}
		return x + y, nil
	}))
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()

	for _, name := range []string{"once", "many"} {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				if _, err := p.Call(context.Background(), name); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestObjectCallback(t *testing.T) {
	p, err := Compile(context.Background(), `
def run():
    return apply(lambda n: n * 3, [1, 2, 3])

def with_method():
    class Doubler:
        def go(self, n):
            return n * 2
    return apply(Doubler().go, [4, 5])

def not_callable():
    return apply(object(), [1])
`, WithFunc("apply", func(c *Call) (any, error) {
		var fn Object
		var items []any
		if err := c.Unpack("fn", &fn, "items", &items); err != nil {
			return nil, err
		}
		out := make([]any, len(items))
		for i, item := range items {
			v, err := fn.Call(item)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	if got, err := p.Call(t.Context(), "run"); err != nil || !reflect.DeepEqual(got, []any{int64(3), int64(6), int64(9)}) {
		t.Errorf("run() = %#v, %v", got, err)
	}
	if got, err := p.Call(t.Context(), "with_method"); err != nil || !reflect.DeepEqual(got, []any{int64(8), int64(10)}) {
		t.Errorf("with_method() = %#v, %v", got, err)
	}

	var exc *Exception
	if _, err := p.Call(t.Context(), "not_callable"); !errors.As(err, &exc) {
		t.Errorf("calling a non-callable = %v, want an exception", err)
	}
}

// An exception raised inside the callback comes back through Go and out the
// other side, rather than being swallowed or taking the interpreter down.
func TestObjectCallbackRaises(t *testing.T) {
	p, err := Compile(context.Background(), `
def run():
    def bad(n):
        raise ValueError("no")
    return apply(bad)

def fine():
    return 1
`, WithFunc("apply", func(c *Call) (any, error) {
		var fn Object
		if err := c.Unpack("fn", &fn); err != nil {
			return nil, err
		}
		return fn.Call(int64(1))
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	var exc *Exception
	if _, err := p.Call(t.Context(), "run"); !errors.As(err, &exc) || exc.Type != "ValueError" {
		t.Errorf("run() = %v, want ValueError", err)
	}
	if got, err := p.Call(t.Context(), "fine"); err != nil || got != int64(1) {
		t.Errorf("after a raising callback: %#v, %v", got, err)
	}
}

// A reference handed back to the guest unchanged, rather than rebuilt.
func TestObjectRoundTrip(t *testing.T) {
	p, err := Compile(context.Background(), `
def run():
    def f():
        return "called"
    got = passthrough(f)
    return [got is f, got()]
`, WithFunc("passthrough", func(c *Call) (any, error) {
		var v any
		return v, c.Unpack("v", &v)
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	got, err := p.Call(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []any{true, "called"}) {
		t.Errorf("run() = %#v, want the same object back", got)
	}
}

// References die with the call that made them, so one that escapes to the host
// says so instead of resolving to whatever now sits at that index.
func TestObjectExpires(t *testing.T) {
	p, err := Compile(context.Background(), `
def make():
    return lambda: 1
`)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	got, err := p.Call(t.Context(), "make")
	if err != nil {
		t.Fatal(err)
	}
	obj, ok := got.(Object)
	if !ok {
		t.Fatalf("make() = %#v (%T), want an Object", got, got)
	}
	if !obj.Callable() {
		t.Error("a lambda came back as not callable")
	}
	if _, err := obj.Call(); err == nil {
		t.Error("a reference outlived the call that produced it")
	}
}

// Bind's raw form: a function that reads the arguments as they arrived, for
// the arity Unpack cannot describe.
func TestFuncRawArgs(t *testing.T) {
	p, err := Compile(context.Background(), `
def run():
    return [total(), total(1, 2, 3), described(x=1)]
`, Declare{
		"total": Fn(func(c *Call) (any, error) {
			var sum int64
			for _, a := range c.Args() {
				n, ok := a.(int64)
				if !ok {
					return nil, fmt.Errorf("%s() takes ints", c.Name())
				}
				sum += n
			}
			return sum, nil
		}),
		"described": Fn(func(c *Call) (any, error) {
			return fmt.Sprintf("%s/%d/%d", c.Name(), len(c.Args()), len(c.Kwargs())), nil
		}),
	}.Option())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	got, err := p.Call(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	want := []any{int64(0), int64(6), "described/0/1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("run() = %#v, want %#v", got, want)
	}
}
