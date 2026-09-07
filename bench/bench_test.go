// Package pybench compares three ways to run Python from Go:
//
//   - micropython: this project. MicroPython compiled to wasm and transpiled to
//     pure Go by wasm2go.
//   - go-python:   github.com/goccy/go-python, real CPython 3.14 through the
//     same wasm2go route.
//   - gpython:     github.com/go-python/gpython, a pure-Go reimplementation of
//     a Python VM (~3.4 semantics).
//
// The first two differ in which interpreter is inside the wasm; the third
// differs in having no wasm at all. MicroPython is a smaller language than
// CPython, so a like-for-like win on these snippets is not a claim about
// feature parity.
//
// All three run the SAME Python source. Each iteration runs the snippet in a
// persistent module or globals, so the engines are measured on equal footing.
//
// Engines are sub-benchmarks, so one run produces every combination and
// benchstat splits them into columns itself:
//
//	go test -run='^$' -bench=. -benchmem -count=10 . > bench.txt
//	go tool benchstat -col /engine bench.txt
//
// Footprint is the exception: see the comment on BenchmarkFootprint.
package pybench

import (
	"runtime"
	"testing"

	python "github.com/goccy/go-python"
	"github.com/gregfurman/micropython-go"

	"github.com/go-python/gpython/compile"
	"github.com/go-python/gpython/py"
	_ "github.com/go-python/gpython/stdlib"
)

// Workloads, identical source for every engine.
const (
	fibDef = `
def fib(n):
    return n if n < 2 else fib(n-1) + fib(n-2)
`
	fibCall = "r = fib(28)" // ~832040 calls of recursive work
	loopSum = `
s = 0
for i in range(200000):
    s += i
`

	loopSumBuiltin = `sum(range(200000))`

	singleOp = `1 + 1`
)

func micropythonInterp(tb testing.TB) *micropython.Instance {
	tb.Helper()
	in, err := micropython.NewInstance(tb.Context())
	if err != nil {
		tb.Fatalf("micropython: %v", err)
	}
	return in
}

func micropythonExec(tb testing.TB, in *micropython.Instance, src string) {
	tb.Helper()
	if err := in.Exec(tb.Context(), src); err != nil {
		tb.Fatalf("micropython exec: %v", err)
	}
}

func goPythonInterp(tb testing.TB) *python.Interpreter {
	tb.Helper()
	interp, err := python.NewInterpreter(python.Config{})
	if err != nil {
		tb.Fatalf("go-python: %v", err)
	}
	return interp
}

func goPythonEval(tb testing.TB, interp *python.Interpreter, src string) {
	tb.Helper()
	r, err := interp.Eval(src)
	if err != nil {
		tb.Fatalf("go-python host error: %v", err)
	}
	if !r.Ok {
		tb.Fatalf("go-python python error: %s", r.Error)
	}
}

func gpythonModule(tb testing.TB, setup string) (py.Context, *py.Module) {
	tb.Helper()
	ctx := py.NewContext(py.DefaultContextOpts())
	code, err := compile.Compile(setup+"\n", "setup", py.ExecMode, 0, true)
	if err != nil {
		tb.Fatalf("gpython setup compile: %v", err)
	}
	mod, err := py.RunCode(ctx, code, "setup", nil)
	if err != nil {
		tb.Fatalf("gpython setup: %v", err)
	}
	return ctx, mod
}

// gpythonExec compiles src in exec mode, so multi-statement snippets run in
// full unlike RunSrc's interactive SingleMode, and runs it in mod's globals.
func gpythonExec(tb testing.TB, ctx py.Context, mod *py.Module, src string) {
	tb.Helper()
	code, _ := compile.Compile(src+"\n", "bench", py.ExecMode, 0, true)
	py.RunCode(ctx, code, "bench", mod)
}

// ---- benchmarks -----------------------------------------------------------

func BenchmarkSimpleArithmetic(b *testing.B) {
	b.Run("engine=micropython", func(b *testing.B) {
		in, _ := micropython.NewInstance(b.Context())

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			in.Exec(b.Context(), singleOp)
		}

		in.Close()
	})

	b.Run("engine=go-python", func(b *testing.B) {
		interp := goPythonInterp(b)

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			goPythonEval(b, interp, singleOp)
		}

		interp.Close()
	})

	b.Run("engine=gpython", func(b *testing.B) {
		ctx, mod := gpythonModule(b, "pass")

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			gpythonExec(b, ctx, mod, singleOp)
		}
		ctx.Close()
	})

}

func BenchmarkFibRecursive(b *testing.B) {
	b.Run("engine=micropython type=exec", func(b *testing.B) {
		in, _ := micropython.NewInstance(b.Context(), micropython.WithSourceScript(fibDef))

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			micropythonExec(b, in, fibCall)
		}
		in.Close()
	})

	b.Run("engine=micropython type=eval", func(b *testing.B) {
		in, _ := micropython.NewInstance(b.Context(), micropython.WithSourceScript(fibDef))

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			in.Eval(b.Context(), "fib(28)")
		}
		in.Close()
	})

	b.Run("engine=go-python", func(b *testing.B) {
		interp := goPythonInterp(b)
		goPythonEval(b, interp, fibDef)

		b.ResetTimer()
		for b.Loop() {
			goPythonEval(b, interp, fibCall)
		}
		interp.Close()
	})

	b.Run("engine=gpython", func(b *testing.B) {
		ctx, mod := gpythonModule(b, fibDef)

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			gpythonExec(b, ctx, mod, fibCall)
		}

		ctx.Close()
	})
}

func BenchmarkLoopSum(b *testing.B) {
	b.Run("engine=micropython", func(b *testing.B) {
		in, _ := micropython.NewInstance(b.Context())

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			in.Exec(b.Context(), loopSumBuiltin)
		}

		in.Close()
	})

	b.Run("engine=go-python", func(b *testing.B) {
		interp := goPythonInterp(b)

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			goPythonEval(b, interp, loopSumBuiltin)
		}

		interp.Close()
	})

	b.Run("engine=gpython", func(b *testing.B) {
		ctx, mod := gpythonModule(b, "pass")

		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			gpythonExec(b, ctx, mod, loopSumBuiltin)
		}
		ctx.Close()
	})
}

func BenchmarkStartup(b *testing.B) {
	b.Run("engine=micropython", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			micropythonInterp(b).Close()
		}
	})

	b.Run("engine=go-python", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			goPythonInterp(b).Close()
		}
	})

	b.Run("engine=gpython", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			ctx, _ := gpythonModule(b, "pass")
			ctx.Close()
		}
	})
}

// ---- footprint ------------------------------------------------------------

// settledHeap runs the collector until it stops finding work, including any
// runtime.AddCleanup callbacks, and reports the live heap.
func settledHeap() uint64 {
	for range 3 {
		runtime.GC()
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// measureFootprint drives build once per iteration and reports the mean heap
// its result retains. build returns the live interpreter, so it can be kept
// reachable across the measurement, and a func to close it.
func measureFootprint(b *testing.B, build func() (live any, closeFn func())) {
	b.ReportAllocs()
	var total int64
	for b.Loop() {
		b.StopTimer()
		before := settledHeap()
		b.StartTimer()

		live, closeFn := build()

		b.StopTimer()
		after := settledHeap()

		// Nothing below reads the interpreter, so without this the collector
		// may reclaim it before the measurement lands.
		runtime.KeepAlive(live)
		closeFn()
		total += int64(after) - int64(before)
		b.StartTimer()
	}
	b.ReportMetric(float64(total)/float64(b.N)/(1024*1024), "MiB/interp")
}

// BenchmarkFootprint reports MiB/interp: the Go heap one live interpreter
// retains after booting and running fib(30). ns/op covers construction and the
// workload only, with the collector settling either side of it excluded.
//
// MiB/interp is not B/op. B/op is bytes allocated, MiB/interp is bytes still
// held once the collector has run, and for these engines the two disagree by
// orders of magnitude in opposite directions.
//
// Unlike the others this one must be run one engine per process, because the Go
// heap does not hand space back between sub-benchmarks and whichever engine
// runs second allocates into room the first already claimed:
//
//	go test -run='^$' -bench='Footprint/engine=micropython' -count=6 .
func BenchmarkFootprint(b *testing.B) {
	b.Run("engine=micropython", func(b *testing.B) {
		measureFootprint(b, func() (any, func()) {
			in := micropythonInterp(b)
			micropythonExec(b, in, fibDef)
			micropythonExec(b, in, "r = fib(30)")
			return in, func() { in.Close() }
		})
	})

	b.Run("engine=go-python", func(b *testing.B) {
		measureFootprint(b, func() (any, func()) {
			interp := goPythonInterp(b)
			goPythonEval(b, interp, fibDef)
			goPythonEval(b, interp, "r = fib(30)")
			return interp, func() { interp.Close() }
		})
	})

	b.Run("engine=gpython", func(b *testing.B) {
		measureFootprint(b, func() (any, func()) {
			ctx, mod := gpythonModule(b, fibDef)
			gpythonExec(b, ctx, mod, "r = fib(30)")
			return ctx, func() { ctx.Close() }
		})
	})
}
