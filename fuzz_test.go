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

	"github.com/gregfurman/micropython-go/internal/value"
)

const guestTimeout = 50 * time.Millisecond

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

// -------------------------------------------------------------

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
		out := make([]any, n)
		for i := range out {
			out[i] = genValue(r, depth+1)
		}
		return value.Tuple(out)
	case 10, 11, 12:
		return value.Set(genHashables(r))
	default:
		n := int(r.byte() % 4)
		out := make([]any, n)
		for i := range out {
			out[i] = genValue(r, depth+1)
		}
		return out
	}
}

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
