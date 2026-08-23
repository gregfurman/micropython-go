package host

import (
	"reflect"
	"testing"
)

func evalValue(t *testing.T, a *ABI, expr string) any {
	t.Helper()
	if err := a.Eval(expr, ModeValue); err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	v, err := a.Value()
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return v
}

func TestDecodeTypes(t *testing.T) {
	a, _ := New(nil)
	for _, tc := range []struct {
		expr string
		want any
	}{
		{"None", nil},
		{"True", true},
		{"1 + 1", int64(2)},
		{"3 / 2", 1.5},
		{"'hi'", "hi"},
		{"b'ab'", []byte("ab")},
		{"[1, 'a', None]", []any{int64(1), "a", nil}},
		{"(1, 2)", Tuple{int64(1), int64(2)}},
		{"{'a': 1, 'b': [2, 3]}", map[string]any{"a": int64(1), "b": []any{int64(2), int64(3)}}},
		{"[]", []any{}},
		{"{}", map[string]any{}},
		{"[[1, [2, [3]]]]", []any{[]any{int64(1), []any{int64(2), []any{int64(3)}}}}},
		{"{'foo': {'bar': {'qux': 'oof'}}}", map[string]any{"foo": map[string]any{"bar": map[string]any{"qux": "oof"}}}},
	} {
		if got := evalValue(t, a, tc.expr); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s = %#v, want %#v", tc.expr, got, tc.want)
		}
	}
}

func TestDecodeOther(t *testing.T) {
	a, _ := New(nil)
	got, ok := evalValue(t, a, "len").(Object)
	if !ok {
		t.Fatalf("got %T, want Object", got)
	}
	if got.Type == "" || got.Repr == "" {
		t.Errorf("got %+v", got)
	}
}
