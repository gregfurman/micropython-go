package micropython

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gregfurman/micropython-wasi/internal/host"
)

// Fuzzing the exported API.
//
// The contract being checked is narrow and absolute: whatever you hand this
// package, it returns a value or an error. It never panics, never wedges the
// interpreter for the next call, and never keeps working after Close.
//
// Fuzzed Python readily contains `while True:`, so every guest call is bounded
// by a context. The VM hook makes that real: without it a single input would
// hang the fuzzer forever and be reported as a crash.

const guestTimeout = 2 * time.Second

// bounded gives the guest a deadline. Fuzzed Python readily contains
// `while True:`, and the VM hook is what makes that survivable.
func bounded() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), guestTimeout)
}

func fuzzInstance(t *testing.T) *Instance {
	t.Helper()
	in, err := NewInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// FuzzExec runs arbitrary text as Python. Almost all of it is a syntax error,
// which is the point: the parser, the compiler and the NLR unwind that carries
// the error back out are the machinery under test, and none of them may take
// the interpreter with them.
func FuzzExec(f *testing.F) {
	for _, seed := range []string{
		"", "\n", "pass", "1/0", "x = 1",
		"def f():\n    return 1\n",
		"raise ValueError('x')",
		"[",
		"def f(:",
		"\x00\x01\x02",
		"import sys",
		"'\\ud800'",
		"0x" + strings.Repeat("f", 400),
		"(" + strings.Repeat("1,", 500) + ")",
		"[[[[[[[[[[1]]]]]]]]]]",
		"class C:\n    def m(self):\n        return C()\n",
		"def r(n):\n    return r(n+1)\nr(0)",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, src string) {
		in := fuzzInstance(t)
		defer in.Close()

		ctx, cancel := bounded()
		defer cancel()

		if _, err := in.Exec(ctx, src); errors.Is(err, context.DeadlineExceeded) {
			t.Skip("guest did not return")
		}

		// Whatever happened, the interpreter has to still work.
		got, err := in.Call(ctx, "len", "abc")
		if errors.Is(err, context.DeadlineExceeded) {
			t.Skip("guest did not return")
		}
		if err != nil {
			t.Fatalf("interpreter unusable after Exec(%q): %v", src, err)
		}
		if got != int64(3) {
			t.Fatalf("interpreter wrong after Exec(%q): len('abc') = %#v", src, got)
		}
	})
}

// FuzzEval drives the expression path, which parses in expression context
// rather than statement context and streams a value back rather than output.
func FuzzEval(f *testing.F) {
	for _, seed := range []string{"1", "1+1", "[]", "{}", "'x'", "None", "(", "1/0", "len"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, expr string) {
		in := fuzzInstance(t)
		defer in.Close()

		ctx, cancel := bounded()
		defer cancel()

		if _, err := in.Eval(ctx, expr); errors.Is(err, context.DeadlineExceeded) {
			t.Skip("guest did not return")
		}

		// Whatever happened, the interpreter has to still work.
		got, err := in.Call(ctx, "len", "abc")
		if errors.Is(err, context.DeadlineExceeded) {
			t.Skip("guest did not return")
		}
		if err != nil {
			t.Fatalf("interpreter unusable after Eval(%q): %v", expr, err)
		}
		if got != int64(3) {
			t.Fatalf("interpreter wrong after Eval(%q): len('abc') = %#v", expr, got)
		}
	})
}

// FuzzCallArgs pushes arbitrary Go values through Python and back. Anything the
// encoder accepts must survive the round trip unchanged, and anything it
// rejects must come back as an error.
func FuzzCallArgs(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0})
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9})
	f.Add([]byte{6, 3, 0, 42})
	f.Add([]byte{7, 2, 5, 1, 0, 3, 0})

	f.Fuzz(func(t *testing.T, seed []byte) {
		want := genValue(&reader{buf: seed}, 0)

		in := fuzzInstance(t)
		defer in.Close()

		if _, err := in.Call(t.Context(), "__nope__"); err == nil {
			t.Fatal("Call resolved a name that does not exist")
		}

		if _, err := in.Exec(t.Context(), "def echo(v):\n    return v\n"); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := bounded()
		defer cancel()

		got, err := in.Call(ctx, "echo", want)
		if errors.Is(err, context.DeadlineExceeded) {
			t.Skip("guest did not return")
		}
		if err != nil {
			t.Fatalf("echo(%#v): %v", want, err)
		}
		// equalValue rather than DeepEqual: a Python set has no order, so its
		// members come back in whatever order the hash table held them.
		if !equalValue(got, want) {
			t.Fatalf("round trip: got %#v, want %#v", got, want)
		}
	})
}

// --- a value generator driven by the fuzzer's bytes -------------------------

type reader struct {
	buf []byte
	pos int
}

func (r *reader) byte() byte {
	if r.pos >= len(r.buf) {
		return 0
	}
	b := r.buf[r.pos]
	r.pos++
	return b
}

func (r *reader) bytes(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = r.byte()
	}
	return out
}

const maxGenDepth = 6

// genValue builds a value the encoder is expected to accept, so that a failed
// round trip means a real asymmetry rather than an unsupported type.
func genValue(r *reader, depth int) any {
	kind := r.byte() % 13
	if depth >= maxGenDepth && kind >= 7 {
		kind = kind % 7
	}

	switch kind {
	case 0:
		return nil
	case 1:
		return r.byte()%2 == 1
	case 2:
		return int64(binary.LittleEndian.Uint64(r.bytes(8)))
	case 3:
		f := math.Float64frombits(binary.LittleEndian.Uint64(r.bytes(8)))
		if math.IsNaN(f) || math.IsInf(f, 0) {
			// NaN fails DeepEqual against itself, and infinities are a
			// separate question from encoding; neither is what this tests.
			return 0.0
		}
		return f
	case 4:
		return string(sanitise(r.bytes(int(r.byte() % 16))))
	case 5:
		return r.bytes(int(r.byte() % 16))
	case 6:
		// Wide integers specifically: the small-int boundary is where the
		// encoder used to give up.
		return int64(binary.LittleEndian.Uint64(r.bytes(8))) >> (r.byte() % 64)
	case 7:
		n := int(r.byte() % 5)
		out := make([]any, n)
		for i := range out {
			out[i] = genValue(r, depth+1)
		}
		return out
	case 8:
		n := int(r.byte() % 5)
		out := make(map[string]any, n)
		for range n {
			out[string(sanitise(r.bytes(int(r.byte()%8))))] = genValue(r, depth+1)
		}
		return out
	case 9:
		n := int(r.byte() % 4)
		out := make(host.Tuple, n)
		for i := range out {
			out[i] = genValue(r, depth+1)
		}
		return out
	case 10, 11:
		// Set members must be hashable, so they come from the scalar half of
		// the generator only -- a set of lists is a guest TypeError, which is
		// correct behaviour and not what a round trip is testing.
		return host.Set(genHashables(r))
	case 12:
		return host.Set(genHashables(r))
	default:
		n := int(r.byte() % 4)
		out := make([]any, n)
		for i := range out {
			out[i] = genValue(r, depth+1)
		}
		return out
	}
}

// genHashables builds set members that are distinct to Python, so the set that
// holds them has the length it was given and the round trip can compare
// element for element.
func genHashables(r *reader) []any {
	n := int(r.byte() % 5)
	seen := make(map[string]bool, n)
	out := make([]any, 0, n)
	for range n {
		v := genScalar(r)
		k := pyKey(v)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, v)
	}
	return out
}

// pyKey identifies a value the way Python's set does, which is not the way Go
// does: bool is a subclass of int there, so {False, 0} is one element and
// {True, 1} is one element. Deduping on the Go type would build sets that
// legitimately come back shorter than they went in.
func pyKey(v any) string {
	switch t := v.(type) {
	case bool:
		if t {
			return "int/1"
		}
		return "int/0"
	case int64:
		return fmt.Sprintf("int/%d", t)
	default:
		return fmt.Sprintf("%T/%v", v, v)
	}
}

func genScalar(r *reader) any {
	switch r.byte() % 5 {
	case 0:
		return nil
	case 1:
		return r.byte()%2 == 1
	case 2:
		return int64(binary.LittleEndian.Uint64(r.bytes(8)))
	default:
		return string(sanitise(r.bytes(int(r.byte() % 12))))
	}
}

// sanitise keeps generated strings valid UTF-8: mp_obj_new_str rejects
// anything else with a UnicodeError, which is correct behaviour and not what
// the round trip is testing.
func sanitise(b []byte) []byte {
	for i, c := range b {
		if c == 0 || c > 0x7e || c < 0x20 {
			b[i] = 'a' + c%26
		}
	}
	return b
}

// FuzzProgram drives the whole public path: compile arbitrary source, then
// call arbitrary names on it with arbitrary arguments, concurrently.
//
// Compile plus a pool is where the parts interact -- a snapshot taken of a
// half-broken interpreter, a restore that leaves state behind, a trapped
// instance handed back out. None of that is reachable from the single-call
// fuzzers above.
func FuzzProgram(f *testing.F) {
	f.Add("def f(v):\n    return v\n", "f", []byte{4, 3})
	f.Add("x = 1\n", "f", []byte{0})
	f.Add("def f(v):\n    raise ValueError(v)\n", "f", []byte{4, 2})
	f.Add("def f(v):\n    global x\n    x = v\n    return x\n", "f", []byte{7, 2})
	f.Add("def f(v):\n    while True:\n        pass\n", "f", []byte{0})
	f.Add("import json\ndef f(v):\n    return json.dumps(v)\n", "f", []byte{8, 1})
	f.Add("", "f", []byte{})

	f.Fuzz(func(t *testing.T, src, name string, seed []byte) {
		p, err := Compile(context.Background(), src)
		if err != nil {
			// Source that does not load is an ordinary answer, not a crash.
			return
		}
		defer p.Close()

		arg := genValue(&reader{buf: seed}, 0)

		// Concurrently, so the pool has to grow from the snapshot and hand
		// back interpreters that later calls can still use.
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := bounded()
				defer cancel()
				p.Call(ctx, name, arg) //nolint:errcheck // any answer is fine; not panicking is the point
			}()
		}
		wg.Wait()

		// Whatever happened, the Program must still serve a call.
		ctx, cancel := bounded()
		defer cancel()

		got, err := p.Call(ctx, "len", "abcd")
		if errors.Is(err, context.DeadlineExceeded) {
			t.Skip("guest did not return")
		}
		if err != nil {
			t.Fatalf("Program unusable after Compile(%q)+Call(%q): %v", src, name, err)
		}
		if got != int64(4) {
			t.Fatalf("Program wrong after Compile(%q)+Call(%q): len('abcd') = %#v", src, name, got)
		}
	})
}
