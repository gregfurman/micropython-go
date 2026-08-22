package micropython

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/gregfurman/micropython-wasi/internal/host"
)

// What survives the crossing, in both directions.
//
// The encoder and decoder are written independently -- one packs a tag stream
// for build/wasm_build.c, the other reassembles what build/wasm_value.c
// streams back -- so nothing but a test holds them to the same idea of a type.
// Both directions are checked because they have failed separately before: Go
// int64 past the small-int boundary was an encoder bug, and Python ints past
// int64 arriving as their low 64 bits was a decoder one.

// TestRoundTrip sends a Go value into Python and gets it back, checking what
// it is on the way out. Several Go types deliberately land on one Python type
// and come back as its canonical Go form, which is why want is separate.
func TestRoundTrip(t *testing.T) {
	in := newT(t)
	if _, err := in.Exec(t.Context(), "def echo(v):\n    return v\n"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		send any
		want any
	}{
		{"none", nil, nil},
		{"true", true, true},
		{"false", false, false},

		// Every Go integer width narrows onto one Python int, and comes back
		// as int64 -- the only Go type that can hold what Python might send.
		{"int", int(7), int64(7)},
		{"int8", int8(-8), int64(-8)},
		{"int16", int16(-16), int64(-16)},
		{"int32", int32(-32), int64(-32)},
		{"int64", int64(-64), int64(-64)},
		{"uint8", uint8(8), int64(8)},
		{"uint16", uint16(16), int64(16)},
		{"uint32", uint32(32), int64(32)},
		{"uint", uint(64), int64(64)},
		{"uint64", uint64(64), int64(64)},
		{"uint64 at int64 max", uint64(1<<63 - 1), int64(1<<63 - 1)},

		{"int64 max", int64(1<<63 - 1), int64(1<<63 - 1)},
		{"int64 min", int64(-1 << 63), int64(-1 << 63)},
		// Past the small-int boundary, which needs MICROPY_LONGINT_IMPL_MPZ.
		{"beyond small int", int64(1) << 40, int64(1) << 40},

		{"float32", float32(0.5), float64(0.5)},
		{"float64", float64(-1.25), float64(-1.25)},
		{"float zero", float64(0), float64(0)},

		{"string", "hello", "hello"},
		{"string empty", "", ""},
		{"string utf8", "héllo → 世界", "héllo → 世界"},
		{"bytes", []byte{0, 1, 255}, []byte{0, 1, 255}},
		{"bytes empty", []byte{}, []byte{}},

		{"list", []any{int64(1), "two"}, []any{int64(1), "two"}},
		{"list empty", []any{}, []any{}},
		{"list nested", []any{[]any{int64(1)}}, []any{[]any{int64(1)}}},

		{"tuple", host.Tuple{int64(1), "two"}, host.Tuple{int64(1), "two"}},
		{"tuple empty", host.Tuple{}, host.Tuple{}},

		{"dict", map[string]any{"a": int64(1)}, map[string]any{"a": int64(1)}},
		{"dict empty", map[string]any{}, map[string]any{}},

		{"set", host.Set{int64(1), int64(2)}, host.Set{int64(1), int64(2)}},
		{"set empty", host.Set{}, host.Set{}},
		{"frozenset", host.FrozenSet{int64(3)}, host.FrozenSet{int64(3)}},

		// Anything the encoder does not name goes through JSON, which is how a
		// struct or a concrete slice reaches Python at all. UseNumber is what
		// keeps the ints from becoming floats on the way.
		{"struct", struct {
			Name string `json:"name"`
			N    int    `json:"n"`
		}{"x", 3}, map[string]any{"name": "x", "n": int64(3)}},
		{"[]int", []int{1, 2}, []any{int64(1), int64(2)}},
		{"map[string]int", map[string]int{"k": 9}, map[string]any{"k": int64(9)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := in.Call(t.Context(), "echo", tt.send)
			if err != nil {
				t.Fatalf("echo(%#v): %v", tt.send, err)
			}
			if !equalValue(got, tt.want) {
				t.Errorf("echo(%#v) = %#v (%T), want %#v (%T)", tt.send, got, got, tt.want, tt.want)
			}
		})
	}
}

// TestRoundTripFromPython is the other direction: a Python literal evaluated
// into a Go value, with no encoder involved.
func TestRoundTripFromPython(t *testing.T) {
	in := newT(t)

	tests := []struct {
		expr string
		want any
	}{
		{"None", nil},
		{"True", true},
		{"0", int64(0)},
		{"-1", int64(-1)},
		{"2 ** 40", int64(1) << 40},
		{"1.5", 1.5},
		{"'text'", "text"},
		{"b'\\x00\\xff'", []byte{0, 255}},
		{"[1, 'a', None]", []any{int64(1), "a", nil}},
		{"(1, 2)", host.Tuple{int64(1), int64(2)}},
		{"()", host.Tuple{}},
		{"{'k': 1}", map[string]any{"k": int64(1)}},
		{"{}", map[string]any{}},
		{"{1, 2}", host.Set{int64(1), int64(2)}},
		{"frozenset({1})", host.FrozenSet{int64(1)}},

		// Nested, so the decoder's frame stack has to close containers in the
		// right order rather than just accumulate.
		{"[{'a': (1, [2])}]", []any{map[string]any{"a": host.Tuple{int64(1), []any{int64(2)}}}}},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := in.Eval(t.Context(), tt.expr)
			if err != nil {
				t.Fatalf("%s: %v", tt.expr, err)
			}
			if !equalValue(got, tt.want) {
				t.Errorf("%s = %#v (%T), want %#v (%T)", tt.expr, got, got, tt.want, tt.want)
			}
		})
	}
}

// TestRoundTripRejects covers what cannot cross, which must be an error rather
// than a wrong value or a dead interpreter.
func TestRoundTripRejects(t *testing.T) {
	in := newT(t)
	if _, err := in.Exec(t.Context(), "def echo(v):\n    return v\n"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		send any
	}{
		{"uint64 past int64", uint64(1 << 63)},
		{"channel", make(chan int)},
		{"func", func() {}},
		{"unhashable set member", host.Set{[]any{int64(1)}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := in.Call(t.Context(), "echo", tt.send); err == nil {
				t.Errorf("echo(%#v) was accepted", tt.send)
			}
			if err := in.Err(); err != nil {
				t.Fatalf("interpreter died: %v", err)
			}
		})
	}
}

// TestRoundTripValuesWithNoGoEquivalent checks the fallback: a Python value the
// host has no type for arrives as its type name and repr rather than failing.
func TestRoundTripValuesWithNoGoEquivalent(t *testing.T) {
	in := newT(t)

	for _, expr := range []string{"len", "Exception", "2 ** 100"} {
		t.Run(expr, func(t *testing.T) {
			got, err := in.Eval(t.Context(), expr)
			if err != nil {
				t.Fatalf("%s: %v", expr, err)
			}
			obj, ok := got.(host.Object)
			if !ok {
				t.Fatalf("%s = %#v (%T), want host.Object", expr, got, got)
			}
			if obj.Type == "" || obj.Repr == "" {
				t.Errorf("%s = %+v, want both Type and Repr set", expr, obj)
			}
		})
	}
}

// equalValue is reflect.DeepEqual except that sets are compared as sets: a
// Python set has no order, so its elements come back in whatever order the
// hash table held them.
//
// It recurses, because a set can be nested anywhere -- inside a tuple, a list
// or a dict value -- and DeepEqual would compare those inner sets by order.
func equalValue(got, want any) bool {
	switch w := want.(type) {
	case host.Set:
		g, ok := got.(host.Set)
		return ok && sameElements(g, w)
	case host.FrozenSet:
		g, ok := got.(host.FrozenSet)
		return ok && sameElements(g, w)

	case host.Tuple:
		g, ok := got.(host.Tuple)
		return ok && sameSequence(g, w)
	case []any:
		g, ok := got.([]any)
		return ok && sameSequence(g, w)

	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok || !equalValue(gv, wv) {
				return false
			}
		}
		return true

	case map[any]any:
		g, ok := got.(map[any]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok || !equalValue(gv, wv) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(got, want)
}

func sameSequence(got, want []any) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if !equalValue(got[i], want[i]) {
			return false
		}
	}
	return true
}

func sameElements(got, want []any) bool {
	if len(got) != len(want) {
		return false
	}
	key := func(items []any) []string {
		out := make([]string, len(items))
		for i, v := range items {
			out[i] = fmt.Sprintf("%T/%v", v, v)
		}
		sort.Strings(out)
		return out
	}
	return reflect.DeepEqual(key(got), key(want))
}
