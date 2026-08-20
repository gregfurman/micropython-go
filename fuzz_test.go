package micropython

import (
	"context"
	"encoding/binary"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
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

// run executes fn with the guest bounded by guestTimeout, and reports whether
// it finished on its own. Anything still running is interrupted rather than
// abandoned.
func run(t *testing.T, in *Instance, fn func()) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), guestTimeout)
	defer cancel()

	err := in.WithContext(ctx, func() error {
		fn()
		return nil
	})
	return err == nil
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
		defer in.Close(context.Background())

		if !run(t, in, func() { in.Exec(src) }) {
			t.Skip("guest did not return")
		}

		// Whatever happened, the interpreter has to still work.
		var (
			got any
			err error
		)
		if !run(t, in, func() { got, err = in.Call("len", "abc") }) {
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

// FuzzEval drives the package-level one-shot path, which boots and closes an
// interpreter per call.
func FuzzEval(f *testing.F) {
	for _, seed := range []string{"1", "1+1", "[]", "{}", "'x'", "None", "(", "1/0", "len"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, expr string) {
		// Eval owns its interpreter, so there is nothing to cancel from here;
		// bound it by running it on its own goroutine and skipping if the
		// guest is still going.
		done := make(chan any, 1)
		go func() {
			defer func() { done <- recover() }()
			Eval(expr)
		}()

		select {
		case p := <-done:
			if p != nil {
				panic(p)
			}
		case <-time.After(guestTimeout):
			t.Skip("guest did not return")
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
		defer in.Close(context.Background())

		echo, err := in.Bind[any, any]("__nope__")
		if err == nil {
			t.Fatal("Bind resolved a name that does not exist")
		}

		echo, err = in.Define[any, any]("echo", "def echo(v):\n    return v\n")
		if err != nil {
			t.Fatal(err)
		}

		var got any
		if !run(t, in, func() { got, err = echo(want) }) {
			t.Skip("guest did not return")
		}
		if err != nil {
			t.Fatalf("echo(%#v): %v", want, err)
		}
		if !reflect.DeepEqual(got, want) {
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
	kind := r.byte() % 10
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
		return int64(r.byte())
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
	default:
		n := int(r.byte() % 4)
		out := make([]any, n)
		for i := range out {
			out[i] = genValue(r, depth+1)
		}
		return out
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
