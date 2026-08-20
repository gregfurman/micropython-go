package host

import (
	"errors"
	"testing"
)

func TestExecOutput(t *testing.T) {
	a := New()
	if err := a.Eval("for i in range(3):\n    print(i)\n", ModeExec); err != nil {
		t.Fatal(err)
	}
	if out := a.Output(); out != "0\n1\n2\n" {
		t.Errorf("out = %q", out)
	}
}

func TestPythonError(t *testing.T) {
	a := New()
	err := a.Eval("1/0", ModeValue)
	if err == nil {
		t.Fatal("expected an error")
	}

	var pyErr *Error
	if !errors.As(err, &pyErr) {
		t.Fatalf("got %T, want *Error", err)
	}
	if pyErr.Text == "" {
		t.Error("empty traceback")
	}

	// The interpreter must still work afterwards.
	if got := evalValue(t, a, "1 + 1"); got != int64(2) {
		t.Errorf("after error: %#v", got)
	}
}

func TestFuncAndCall(t *testing.T) {
	a := New()
	if err := a.Eval("def add(x, y):\n    return x + y\n", ModeExec); err != nil {
		t.Fatal(err)
	}

	handle, err := a.Func("add")
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.Call(handle, []any{20, 22})
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(42) {
		t.Errorf("add(20, 22) = %#v, want 42", got)
	}

	if _, err := a.Func("nope"); err == nil {
		t.Error("expected an error resolving a missing name")
	}
	if _, err := a.Func("add"); err != nil {
		t.Errorf("resolving after a failure: %v", err)
	}
}
