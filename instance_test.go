package micropython

import (
	"context"
	"reflect"
	"testing"
)

// Everything here goes through the exported surface. The wrapper is written by
// hand, so this is what stops a forwarding mistake -- a method calling itself,
// or dropping its type arguments -- from shipping.

func newT(t *testing.T) *Instance {
	t.Helper()
	in, err := NewInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(context.Background()) })
	return in
}

func TestDefineAndCall(t *testing.T) {
	in := newT(t)

	score, err := in.Define[map[string]any, map[string]any]("score", `
def score(row):
    total = row["a"] * 2 + row["b"]
    return {"id": row["id"], "score": total, "ok": total > 10}
`)
	if err != nil {
		t.Fatal(err)
	}

	got, err := score(map[string]any{"id": "r-1", "a": 4, "b": 5})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": "r-1", "score": int64(13), "ok": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("score = %#v, want %#v", got, want)
	}
}

func TestExecThenBind(t *testing.T) {
	in := newT(t)

	if _, err := in.Exec("def double(n):\n    return n * 2\n\ndef shout(s):\n    return s.upper()\n"); err != nil {
		t.Fatal(err)
	}

	double, err := in.Bind[int, int]("double")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := double(21); err != nil || got != 42 {
		t.Errorf("double(21) = %v, %v", got, err)
	}

	shout, err := in.Bind[string, string]("shout")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := shout("hi"); err != nil || got != "HI" {
		t.Errorf("shout = %q, %v", got, err)
	}
}

func TestExecOutput(t *testing.T) {
	in := newT(t)
	out, err := in.Exec("print('hello')\n")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello\n" {
		t.Errorf("out = %q", out)
	}
}

func TestCallByName(t *testing.T) {
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

// Close must release the interpreter and must not recurse.
func TestClose(t *testing.T) {
	in, err := NewInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Call("print"); err == nil {
		t.Error("expected an error after Close")
	}
	if _, err := in.Exec("pass"); err == nil {
		t.Error("expected an error after Close")
	}
}

func TestPythonError(t *testing.T) {
	in := newT(t)
	if _, err := in.Exec("1/0\n"); err == nil {
		t.Fatal("expected a Python error")
	}
	// The instance must still work afterwards.
	if got, err := in.Call("len", "abc"); err != nil || got != int64(3) {
		t.Errorf("after error: %#v, %v", got, err)
	}
}

func TestEval(t *testing.T) {
	got, err := Eval("[1, 2, 3]")
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{int64(1), int64(2), int64(3)}; !reflect.DeepEqual(got, want) {
		t.Errorf("Eval = %#v, want %#v", got, want)
	}
}
