package host

import (
	"reflect"
	"testing"
)

// echo returns a handle to a Python function that hands its argument straight
// back, so a call exercises the encoder and the decoder end to end.
func echo(t *testing.T) (*ABI, int32) {
	t.Helper()
	a := New()
	if err := a.Eval("def echo(v):\n    return v\n", ModeExec); err != nil {
		t.Fatal(err)
	}
	handle, err := a.Func("echo")
	if err != nil {
		t.Fatal(err)
	}
	return a, handle
}

func TestEncodeRoundTrip(t *testing.T) {
	a, handle := echo(t)
	for _, want := range []any{
		nil, true, int64(42), 1.5, "text with 'quotes' and \\ and \n",
		[]byte{0, 1, 2, 255},
		[]any{int64(1), "two", nil, []any{true}},
		map[string]any{"k": "v", "n": int64(3)},
		Tuple{int64(1), "two"},
		[]any{}, map[string]any{},
	} {
		got, err := a.Call(handle, []any{want})
		if err != nil {
			t.Errorf("echo(%#v): %v", want, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("round trip: got %#v, want %#v", got, want)
		}
	}
}

// Concrete container types reach Python through the encoder's JSON fallback,
// which has to keep whole numbers integers rather than making them float64.
func TestEncodeConcreteGoTypes(t *testing.T) {
	a, handle := echo(t)
	for _, tc := range []struct {
		in   any
		want any
	}{
		{[]int{1, 2, 3}, []any{int64(1), int64(2), int64(3)}},
		{[]string{"a", "b"}, []any{"a", "b"}},
		{map[string]string{"a": "b"}, map[string]any{"a": "b"}},
		{map[string]int{"n": 7}, map[string]any{"n": int64(7)}},
		{struct {
			A int     `json:"a"`
			B float64 `json:"b"`
		}{1, 2.5}, map[string]any{"a": int64(1), "b": 2.5}},
	} {
		got, err := a.Call(handle, []any{tc.in})
		if err != nil {
			t.Errorf("echo(%#v): %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("echo(%#v) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

// Arguments are encoded, never pasted into source; this is what that buys.
func TestNoInjection(t *testing.T) {
	a, handle := echo(t)
	evil := "'; import sys; x = 'pwned"
	got, err := a.Call(handle, []any{evil})
	if err != nil {
		t.Fatal(err)
	}
	if got != evil {
		t.Errorf("got %#v, want %#v", got, evil)
	}
}

func TestEncodeTooDeep(t *testing.T) {
	a, handle := echo(t)
	deep := any(int64(1))
	for range maxEncodeDepth + 2 {
		deep = []any{deep}
	}
	if _, err := a.Call(handle, []any{deep}); err == nil {
		t.Error("expected a depth error")
	}
}

func TestEncodeMultipleArgs(t *testing.T) {
	a := New()
	if err := a.Eval("def join(a, b, c):\n    return [a, b, c]\n", ModeExec); err != nil {
		t.Fatal(err)
	}
	handle, err := a.Func("join")
	if err != nil {
		t.Fatal(err)
	}

	got, err := a.Call(handle, []any{int64(1), "two", nil})
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{int64(1), "two", nil}; !reflect.DeepEqual(got, want) {
		t.Errorf("join = %#v, want %#v", got, want)
	}
}
