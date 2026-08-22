package micropython

import (
	"context"
	"reflect"
	"testing"
)

func newT(t *testing.T) *Instance {
	t.Helper()
	in, err := NewInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close() })
	return in
}

func TestExecThenCall(t *testing.T) {
	ctx := context.Background()
	in := newT(t)

	if _, err := in.Exec(ctx, "def double(n):\n    return n * 2\n\ndef shout(s):\n    return s.upper()\n"); err != nil {
		t.Fatal(err)
	}

	if got, err := in.Call(ctx, "double", 21); err != nil || got != int64(42) {
		t.Errorf("double(21) = %#v, %v", got, err)
	}
	if got, err := in.Call(ctx, "shout", "hi"); err != nil || got != "HI" {
		t.Errorf("shout(\"hi\") = %#v, %v", got, err)
	}
}

func TestExecOutput(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	out, err := in.Exec(ctx, "print('hello')\n")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello\n" {
		t.Errorf("out = %q", out)
	}
}

func TestCallByName(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	if _, err := in.Exec(ctx, "def add(a, b):\n    return a + b\n"); err != nil {
		t.Fatal(err)
	}
	got, err := in.Call(ctx, "add", 20, 22)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(42) {
		t.Errorf("add(20, 22) = %#v, want 42", got)
	}
}

func TestClose(t *testing.T) {
	ctx := context.Background()
	in, err := NewInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Call(ctx, "print"); err == nil {
		t.Error("expected an error after Close")
	}
	if _, err := in.Exec(ctx, "pass"); err == nil {
		t.Error("expected an error after Close")
	}
}

func TestPythonError(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	if _, err := in.Exec(ctx, "1/0\n"); err == nil {
		t.Fatal("expected a Python error")
	}
	// The instance must still work afterwards.
	if got, err := in.Call(ctx, "len", "abc"); err != nil || got != int64(3) {
		t.Errorf("after error: %#v, %v", got, err)
	}
}

func TestEval(t *testing.T) {
	got, err := newT(t).Eval(t.Context(), "[1, 2, 3]")
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{int64(1), int64(2), int64(3)}; !reflect.DeepEqual(got, want) {
		t.Errorf("Eval = %#v, want %#v", got, want)
	}
}
