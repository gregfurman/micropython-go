package exec

import (
	"reflect"
	"testing"
)

func newT(t *testing.T) *Instance {
	t.Helper()
	in, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestEvalTypes(t *testing.T) {
	in := newT(t)
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
	} {
		got, err := in.Eval(tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s = %#v, want %#v", tc.expr, got, tc.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	in := newT(t)
	for _, value := range []any{
		nil, true, int64(42), 1.5, "text with 'quotes' and \\ and \n",
		[]byte{0, 1, 2, 255},
		[]any{int64(1), "two", nil, []any{true}},
		map[string]any{"k": "v", "n": int64(3)},
	} {
		if err := in.Set("x", value); err != nil {
			t.Errorf("Set(%#v): %v", value, err)
			continue
		}
		got, err := in.Eval("x")
		if err != nil {
			t.Errorf("Eval after Set(%#v): %v", value, err)
			continue
		}
		if !reflect.DeepEqual(got, value) {
			t.Errorf("round trip: got %#v, want %#v", got, value)
		}
	}
}

func TestConcreteGoTypes(t *testing.T) {
	in := newT(t)
	if err := in.Set("nums", []int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	got, err := TypedEval[int64](in, "sum(nums)")
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(6) {
		t.Errorf("sum = %#v, want 6", got)
	}

	if err := in.Set("m", map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	if got, err := in.Eval("m['a']"); err != nil || got != "b" {
		t.Errorf("m['a'] = %#v, %v", got, err)
	}
}

func TestCall(t *testing.T) {
	in := newT(t)
	if _, err := in.Exec("def add(a, b):\n    return a + b\n"); err != nil {
		t.Fatal(err)
	}
	got, err := in.Call("add", 20, 22)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(42) {
		t.Errorf("add(20, 22) = %#v, want 42", got)
	}
}

func TestNoInjection(t *testing.T) {
	in := newT(t)
	// Would be catastrophic if arguments were pasted into source.
	evil := "'; import sys; x = 'pwned"
	if err := in.Set("s", evil); err != nil {
		t.Fatal(err)
	}
	got, err := in.Eval("s")
	if err != nil {
		t.Fatal(err)
	}
	if got != evil {
		t.Errorf("got %#v, want %#v", got, evil)
	}
}

func TestPythonError(t *testing.T) {
	in := newT(t)
	_, err := in.Eval("1/0")
	if err == nil {
		t.Fatal("expected an error")
	}
	var pyErr *Error
	if !as(err, &pyErr) {
		t.Fatalf("got %T, want *Error", err)
	}
	if pyErr.Text == "" {
		t.Error("empty traceback")
	}
	// The instance must still work afterwards.
	if got, err := in.Eval("1 + 1"); err != nil || got != int64(2) {
		t.Errorf("after error: %#v, %v", got, err)
	}
}

func as(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

func TestExecOutput(t *testing.T) {
	in := newT(t)
	out, err := in.Exec("for i in range(3):\n    print(i)\n")
	if err != nil {
		t.Fatal(err)
	}
	if out != "0\n1\n2\n" {
		t.Errorf("out = %q", out)
	}
}
