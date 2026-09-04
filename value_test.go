package micropython

import (
	"context"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gregfurman/micropython-go/internal/value"
)

func TestRoundTrip(t *testing.T) {
	in := newT(t)
	if err := in.Exec(t.Context(), "def echo(v):\n    return v\n"); err != nil {
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

		{"tuple", Tuple(Int(1), Str("two")), value.Tuple{int64(1), "two"}},
		{"tuple empty", Tuple(), value.Tuple{}},

		{"map", map[string]any{"a": int64(1)}, map[string]any{"a": int64(1)}},
		{"map empty", map[string]any{}, map[string]any{}},
		{"dict", Dict(Item{Key: Str("a"), Val: Int(1)}), map[string]any{"a": int64(1)}},
		{"dict empty", Dict(), map[string]any{}},

		{"set", Set(Int(1), Int(2)), value.Set{int64(1), int64(2)}},
		{"set empty", Set(), value.Set{}},
		{"frozenset", FrozenSet(Int(3)), value.FrozenSet{int64(3)}},

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
			if !equalValue(got.Export(), tt.want) {
				t.Errorf("echo(%#v) = %#v (%T), want %#v (%T)", tt.send, got, got, tt.want, tt.want)
			}
		})
	}
}

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

		{"(1, 2)", value.Tuple{int64(1), int64(2)}},
		{"()", value.Tuple{}},
		{"{'k': 1}", map[string]any{"k": int64(1)}},
		{"{}", map[string]any{}},
		{"{1, 2}", value.Set{int64(1), int64(2)}},
		{"frozenset({1})", value.FrozenSet{int64(1)}},
		{"[{'a': (1, [2])}]", []any{map[string]any{"a": value.Tuple{int64(1), []any{int64(2)}}}}},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			got, err := in.Eval(t.Context(), tt.expr)
			if err != nil {
				t.Fatalf("%s: %v", tt.expr, err)
			}
			if !equalValue(got.Export(), tt.want) {
				t.Errorf("%s = %#v (%T), want %#v (%T)", tt.expr, got, got, tt.want, tt.want)
			}
		})
	}
}

func TestRoundTripRejects(t *testing.T) {
	in := newT(t)
	if err := in.Exec(t.Context(), "def echo(v):\n    return v\n"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		send any
	}{
		{"uint64 past int64", uint64(1 << 63)},
		{"channel", make(chan int)},
		{"func", func() {}},
		{"unhashable set member", Set(List(Int(1)))},
		// {"empty interface", new(any)},
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

func TestRoundTripValuesWithNoGoEquivalent(t *testing.T) {
	in := newT(t)

	// A value only the guest understands keeps its type name and repr.
	for _, expr := range []string{"object()", "2 ** 100"} {
		t.Run(expr, func(t *testing.T) {
			got, err := in.Eval(t.Context(), expr)
			if err != nil {
				t.Fatalf("%s: %v", expr, err)
			}
			obj, err := got.AsObject()
			if err != nil {
				t.Fatalf("%s = %#v, want an Object: %v", expr, got, err)
			}
			if obj.Type() == "" || obj.Repr() == "" {
				t.Errorf("%s = %+v, want both Type and Repr set", expr, obj)
			}
		})
	}

	// A callable instead crosses as a handle, so it comes back callable.
	for _, expr := range []string{"len", "Exception", "lambda x: x"} {
		t.Run(expr, func(t *testing.T) {
			got, err := in.Eval(t.Context(), expr)
			if err != nil {
				t.Fatalf("%s: %v", expr, err)
			}
			if !got.IsCallable() {
				t.Fatalf("%s = %#v, want a callable", expr, got)
			}
			if _, err := in.AsCallable(got); err != nil {
				t.Errorf("%s: AsCallable: %v", expr, err)
			}
		})
	}
}

func TestGuestReferenceSurvivesCollection(t *testing.T) {
	in := newT(t)

	// The returned closure is not stored in a Python global.  Once make_adder
	// returns, the host reference is therefore its only intended root.
	if err := in.Exec(t.Context(), `
def make_adder():
    base = 40
    return lambda n: base + n

def collect_garbage():
    # Create unreachable objects as well as explicitly collecting, so this
    # does not merely exercise a no-op collection.
    garbage = [bytearray(256) for _ in range(32)]
    del garbage
    import gc
    gc.collect()
`); err != nil {
		t.Fatal(err)
	}

	// Keep the Python value only in Go.  value_from_obj has placed its ref in
	// host_refs, which is an MP_REGISTER_ROOT_POINTER.
	held, err := in.Call(t.Context(), "make_adder")
	if err != nil {
		t.Fatal(err)
	}
	if !held.IsCallable() {
		t.Fatalf("make_adder() = %#v, want a callable", held)
	}

	if _, err := in.Call(t.Context(), "collect_garbage"); err != nil {
		t.Fatalf("collecting garbage: %v", err)
	}

	fn, err := in.AsCallable(held)
	if err != nil {
		t.Fatalf("the held value lost its guest reference: %v", err)
	}
	got, err := fn.Call(t.Context(), 2)
	if err != nil {
		t.Fatalf("calling the held value after collection: %v", err)
	}
	if got.Export() != int64(42) {
		t.Errorf("held callable returned %#v, want 42", got.Export())
	}
}

func TestFrozenSetIsImmutable(t *testing.T) {
	in := newT(t)

	if err := in.Exec(t.Context(), "def mutate(v):\n    v.add(99)\n"); err != nil {
		t.Fatal(err)
	}

	if _, err := in.Call(t.Context(), "mutate", FrozenSet(Int(1))); err == nil {
		t.Error("a frozenset accepted add() but should be immutable")
	} else if !strings.Contains(err.Error(), "AttributeError") {
		t.Errorf("mutating a frozenset: %v, want AttributeError", err)
	}

	// A set is the mutable one, so the same call succeeds.
	if _, err := in.Call(t.Context(), "mutate", Set(Int(1))); err != nil {
		t.Errorf("a set refused add(): %v", err)
	}
}

func TestCompositeDictKeys(t *testing.T) {
	for _, expr := range []string{
		"{(1, 2): 'x'}",
		"{frozenset({1}): 'y'}",
		"{b'k': 'z'}",
		"{(1, 2): 'x', 'plain': 'v'}",
	} {
		t.Run(expr, func(t *testing.T) {
			in := newT(t)

			got, err := in.Eval(t.Context(), expr)
			if err != nil {
				t.Fatalf("%s: %v", expr, err)
			}
			if _, ok := got.Export().(map[any]any); !ok {
				t.Errorf("%s = %#v (%T), want map[any]any", expr, got, got)
			}
		})
	}
}

func TestBigIntsDoNotTruncate(t *testing.T) {
	in, _ := NewInstance(context.Background())
	defer in.Close()

	for _, tc := range []struct{ expr, want string }{
		{"1 << 100", "1267650600228229401496703205376"},
		{"(1<<70) + 1", "1180591620717411303425"},
		{"int('9'*30)", "999999999999999999999999999999"},
		{"-(1 << 100)", "-1267650600228229401496703205376"},
		{"2**64", "18446744073709551616"},
		{"2**63 - 1", "9223372036854775807"}, // largest that still fits int64
		{"-(2**63)", "-9223372036854775808"},
	} {
		got, err := in.Eval(t.Context(), tc.expr)
		if err != nil {
			t.Errorf("%s -> %v", tc.expr, err)
			continue
		}

		var text string
		switch v := got.Export().(type) {
		case int64:
			text = big.NewInt(v).String()
		default:
			text = stringOf(v)
		}
		if text != tc.want {
			t.Errorf("%-16s = %v (%T), want %s", tc.expr, got, got, tc.want)
		}
	}
}

func TestIterator(t *testing.T) {
	iterableClass := `
class MyNumbers:
  def __iter__(self):
    self.a = 1
    return self

  def __next__(self):
    if self.a >= 5:
      raise StopIteration
    
    x = self.a
    self.a += 1
    return x
`

	tests := []struct {
		name string

		expr   string
		before string

		want []any

		wantErr bool
	}{
		{
			name: "builtin tuple iter",
			expr: `iter(("hello","cruel","world"))`,
			want: []any{"hello", "cruel", "world"},
		},
		{
			name: "builtin enumerate",
			expr: `enumerate(["hello", "cruel", "world"])`,
			want: []any{[]any{int64(0), "hello"}, []any{int64(1), "cruel"}, []any{int64(2), "world"}},
		},
		{
			name: "anonymous generator",
			expr: `(x ** 2 for x in range(1, 5))`,
			want: []any{int64(1), int64(4), int64(9), int64(16)},
		},
		{
			name:   "custom __iter__ class",
			expr:   `iter(MyNumbers())`,
			want:   []any{int64(1), int64(2), int64(3), int64(4)},
			before: iterableClass,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := newT(t)

			if tt.before != "" {
				err := in.Exec(t.Context(), tt.before)
				if err != nil {
					t.Fatalf("unexpected error when executing before script: %s", err)
				}

			}

			val, err := in.Eval(t.Context(), tt.expr)
			if err != nil {
				t.Fatalf("unexpected error when evaluating %q: %v", tt.expr, err)
			}

			if !val.IsIterator() {
				t.Fatalf("expected iterator type, got %q", val.Type())
			}

			gen, err := in.AsIterator(val)
			if err != nil {
				t.Fatalf("failed to bind iterator to instance: %v", err)
			}

			var got []any
			for val, err := range gen.Iter(t.Context()) {
				if err != nil {
					t.Fatalf("failed to generate next iteration: %v", err)
				}

				res := val.Export()
				got = append(got, res)
			}

			if !sameSequence(got, tt.want) {
				t.Errorf("expected: %+v, got %+v", tt.want, got)
				return
			}

		})
	}
}

func TestCallable(t *testing.T) {
	tests := []struct {
		name string

		expr   string
		before string

		want  any
		input any

		wantErr bool
	}{
		{
			name:  "builtin sum",
			expr:  `sum`,
			input: []int{1, 2, 3},
			want:  6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := newT(t)

			if tt.before != "" {
				err := in.Exec(t.Context(), tt.before)
				if err != nil {
					t.Fatalf("unexpected error when executing before script: %s", err)
				}

			}

			val, err := in.Eval(t.Context(), tt.expr)
			if err != nil {
				t.Fatalf("unexpected error when evaluating %q: %v", tt.expr, err)
			}

			if !val.IsCallable() {
				t.Fatalf("expected iterator type, got %q", val.Type())
			}

			fn, err := in.AsCallable(val)
			if err != nil {
				t.Fatalf("failed to bind iterator to instance: %v", err)
			}

			got, err := fn.Call(t.Context(), tt.input)
			if err != nil {
				t.Fatalf("failed to generate next iteration: %v", err)
			}

			if got == tt.want {
				t.Errorf("expected: %+v, got %+v", tt.want, got)
				return
			}

		})
	}
}

func stringOf(v any) string {
	if s, ok := v.(interface{ String() string }); ok {
		return s.String()
	}
	return ""
}

func equalValue(got, want any) bool {
	switch w := want.(type) {
	case value.Set:
		g, ok := got.([]any)
		return ok && sameElements(g, []any(w))
	case value.FrozenSet:
		g, ok := got.([]any)
		return ok && sameElements(g, []any(w))

	case value.Tuple:
		g, ok := got.([]any)
		return ok && sameSequence(g, []any(w))
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
