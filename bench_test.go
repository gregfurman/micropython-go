package micropython

import (
	"context"
	"fmt"
	"runtime"
	"testing"
)

// What the numbers are for.
//
// Three costs matter and they differ by orders of magnitude: building an
// interpreter (~100µs), rewinding one to a snapshot (~50µs), and making a call
// (~1µs). Which of those a design pays per request is the whole question --
// it is why Program pools interpreters instead of compiling per call, and why
// release rewinds rather than rebuilding.

const benchSrc = `
import json

def noop():
    pass

def echo(v):
    return v

def add(a, b):
    return a + b

def work(n):
    total = 0
    for i in range(n):
        total += i
    return total

def handle(req):
    body = json.loads(req["body"])
    return {"ok": True, "n": len(body.get("items", []))}
`

func benchProgram(b *testing.B) *Program {
	b.Helper()
	p, err := Compile(context.Background(), benchSrc)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { p.Close() })
	return p
}

func benchInstance(b *testing.B) *Instance {
	b.Helper()
	in, err := NewInstance(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	if _, err := in.Exec(context.Background(), benchSrc); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { in.Close() })
	return in
}

// --- startup ---------------------------------------------------------------

// The three ways to get an interpreter with the source loaded, which is the
// comparison Program's design rests on.
func BenchmarkStartup(b *testing.B) {
	ctx := context.Background()

	b.Run("NewInstance", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			in, err := NewInstance(ctx)
			if err != nil {
				b.Fatal(err)
			}
			in.Close()
		}
	})

	b.Run("NewInstance+Exec", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			in, err := NewInstance(ctx)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := in.Exec(ctx, benchSrc); err != nil {
				b.Fatal(err)
			}
			in.Close()
		}
	})

	b.Run("Compile", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			p, err := Compile(ctx, benchSrc)
			if err != nil {
				b.Fatal(err)
			}
			p.Close()
		}
	})
}

// --- calls -----------------------------------------------------------------

// Call cost by argument shape, against a single interpreter, so the numbers
// are the crossing itself with no pooling or rewinding in them.
func BenchmarkCall(b *testing.B) {
	in := benchInstance(b)
	ctx := context.Background()

	cases := []struct {
		name string
		fn   string
		args []any
	}{
		{"no args", "noop", nil},
		{"one int", "echo", []any{Int(42)}},
		{"two ints", "add", []any{Int(20), Int(22)}},
		{"short string", "echo", []any{Str("hello")}},
		{"1KB string", "echo", []any{Str(string(make([]byte, 1024)))}},
		{"1KB bytes", "echo", []any{Bytes(make([]byte, 1024))}},
		{"small list", "echo", []any{List(Int(1), Int(2), Int(3))}},
		{"100 ints", "echo", []any{Of(hundredInts())}},
		{"small dict", "echo", []any{Dict(Item{Key: Str("a"), Val: Int(1)}, Item{Key: Str("b"), Val: Str("two")})}},
		{"nested", "echo", []any{Of(nested())}},
		{"tuple", "echo", []any{Tuple(Int(1), Int(2))}},
		{"set", "echo", []any{Set(Int(1), Int(2), Int(3))}},
		{"struct via json", "echo", []any{Of(struct {
			A int    `json:"a"`
			B string `json:"b"`
		}{1, "two"})}},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := in.Call(ctx, tc.fn, tc.args...); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Eval and Exec against the same trivial work, since they take different paths
// through the module: one streams a value back, the other captures output.
func BenchmarkEvalExec(b *testing.B) {
	in := benchInstance(b)
	ctx := context.Background()

	b.Run("Eval expression", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := in.Eval(ctx, "1 + 1"); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Exec statement", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := in.Exec(ctx, "x = 1 + 1"); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Exec with output", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := in.Exec(ctx, "print('hello')"); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// The cost of a call that fails, which is not the same as one that succeeds:
// it unwinds through nlr, formats a traceback, and asks the module to take the
// exception apart.
func BenchmarkError(b *testing.B) {
	in := newBenchInstanceWith(b, "def boom():\n    raise ValueError('boom')\n")
	ctx := context.Background()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := in.Call(ctx, "boom"); err == nil {
			b.Fatal("expected an error")
		}
	}
}

// --- guest work ------------------------------------------------------------

// Where the crossing stops mattering. At 100k iterations the interpreter is
// doing the work and the ~1µs of ABI is noise, which is the regime a real
// handler runs in.
func BenchmarkGuestWork(b *testing.B) {
	in := benchInstance(b)
	ctx := context.Background()

	for _, n := range []int64{100, 10_000, 1_000_000} {
		b.Run(fmt.Sprintf("range %d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := in.Call(ctx, "work", n); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// The VM hook runs every MICROPY_VM_HOOK_COUNT bytecodes to ask whether to
// stop. This is what it costs when nothing is cancelling: a call with a
// context that can never fire still arms one, a call with a background context
// does not.
func BenchmarkCancellationOverhead(b *testing.B) {
	in := benchInstance(b)

	b.Run("background ctx", func(b *testing.B) {
		ctx := context.Background()
		b.ReportAllocs()
		for b.Loop() {
			if _, err := in.Call(ctx, "work", int64(10_000)); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("cancellable ctx", func(b *testing.B) {
		ctx := b.Context()
		b.ReportAllocs()
		for b.Loop() {
			if _, err := in.Call(ctx, "work", int64(10_000)); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// --- Program ---------------------------------------------------------------

// A Program call includes the rewind that keeps the pool clean, so this is the
// per-request cost of the isolation Program provides. Against Instance.Call,
// the difference is what that isolation costs.
func BenchmarkProgramVsInstance(b *testing.B) {
	ctx := context.Background()

	b.Run("Instance.Call", func(b *testing.B) {
		in := benchInstance(b)
		b.ReportAllocs()
		for b.Loop() {
			if _, err := in.Call(ctx, "add", int64(1), int64(2)); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Program.Call", func(b *testing.B) {
		p := benchProgram(b)
		b.ReportAllocs()
		for b.Loop() {
			if _, err := p.Call(ctx, "add", int64(1), int64(2)); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Parallel throughput. An Instance serialises -- one linear memory, one call at
// a time -- while a Program grows its pool, so these should diverge with GOMAXPROCS.
func BenchmarkParallel(b *testing.B) {
	ctx := context.Background()
	b.Logf("GOMAXPROCS=%d", runtime.GOMAXPROCS(0))

	b.Run("Instance", func(b *testing.B) {
		in := benchInstance(b)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if _, err := in.Call(ctx, "work", int64(1000)); err != nil {
					b.Fatal(err)
				}
			}
		})
	})

	b.Run("Program", func(b *testing.B) {
		p := benchProgram(b)
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				if _, err := p.Call(ctx, "work", int64(1000)); err != nil {
					b.Fatal(err)
				}
			}
		})
	})
}

// End to end: the shape a host actually serves, with json parsing on both
// sides of the boundary.
func BenchmarkHandler(b *testing.B) {
	p := benchProgram(b)
	ctx := context.Background()
	req := map[string]any{"body": `{"items": [1, 2, 3, 4, 5], "who": "bench"}`}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := p.Call(ctx, "handle", req); err != nil {
			b.Fatal(err)
		}
	}
}

// Serving one Program from many goroutines, which is what a request handler
// does. The pool has to grow and be handed back without contention showing up
// as latency.
func BenchmarkHandlerParallel(b *testing.B) {
	p := benchProgram(b)
	ctx := context.Background()
	req := map[string]any{"body": `{"items": [1, 2, 3, 4, 5], "who": "bench"}`}

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := p.Call(ctx, "handle", req); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Many Programs live at once, the way a host with several tenants would hold
// them, to show whether they interfere.
func BenchmarkManyPrograms(b *testing.B) {
	ctx := context.Background()

	for _, n := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("%d programs", n), func(b *testing.B) {
			programs := make([]*Program, n)
			for i := range programs {
				p, err := Compile(ctx, benchSrc)
				if err != nil {
					b.Fatal(err)
				}
				defer p.Close()
				programs[i] = p
			}

			b.ReportAllocs()
			b.ResetTimer()

			var i int
			for b.Loop() {
				if _, err := programs[i%n].Call(ctx, "add", int64(1), int64(2)); err != nil {
					b.Fatal(err)
				}
				i++
			}
		})
	}
}

// --- helpers ---------------------------------------------------------------

func newBenchInstanceWith(b *testing.B, src string) *Instance {
	b.Helper()
	in, err := NewInstance(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	if _, err := in.Exec(context.Background(), src); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { in.Close() })
	return in
}

func hundredInts() []any {
	out := make([]any, 100)
	for i := range out {
		out[i] = int64(i)
	}
	return out
}

func nested() any {
	return map[string]any{
		"id":   "r-1",
		"tags": []any{"a", "b", "c"},
		"meta": map[string]any{"n": int64(1), "deep": []any{map[string]any{"x": true}}},
	}
}
