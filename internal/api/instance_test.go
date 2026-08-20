package api

import (
	"errors"
	"testing"
)

func TestClose(t *testing.T) {
	in := instance(t)
	if _, err := in.Exec("x = 1\n"); err != nil {
		t.Fatal(err)
	}

	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	if err := in.Close(); err != nil {
		t.Errorf("Close is not idempotent: %v", err)
	}

	if _, err := in.Eval("1"); !errors.Is(err, ErrClosed) {
		t.Errorf("Eval after Close: %v, want ErrClosed", err)
	}
	if _, err := in.Exec("pass"); !errors.Is(err, ErrClosed) {
		t.Errorf("Exec after Close: %v, want ErrClosed", err)
	}
	if _, err := in.Func("print"); !errors.Is(err, ErrClosed) {
		t.Errorf("Func after Close: %v, want ErrClosed", err)
	}
	if err := in.Reset(); !errors.Is(err, ErrClosed) {
		t.Errorf("Reset after Close: %v, want ErrClosed", err)
	}
}

func TestReset(t *testing.T) {
	in := instance(t)
	if _, err := in.Exec("x = 41\ndef bump():\n    return x + 1\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := in.Eval("bump()"); err != nil || got != int64(42) {
		t.Fatalf("before reset: %#v, %v", got, err)
	}

	if err := in.Reset(); err != nil {
		t.Fatal(err)
	}

	// Every global and definition is gone.
	if _, err := in.Eval("x"); err == nil {
		t.Error("x survived the reset")
	}
	if _, err := in.Eval("bump()"); err == nil {
		t.Error("bump survived the reset")
	}

	// And the interpreter still works.
	if _, err := in.Exec("y = 1\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := in.Eval("y"); err != nil || got != int64(1) {
		t.Errorf("after reset: %#v, %v", got, err)
	}
}

// A handle indexes a table belonging to one interpreter, so it must not be
// usable against the one that replaces it.
func TestFuncStaleAfterReset(t *testing.T) {
	in := instance(t)
	if _, err := in.Exec("def add(a, b):\n    return a + b\n"); err != nil {
		t.Fatal(err)
	}
	fn, err := in.Func("add")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := fn.Call(20, 22); err != nil || got != int64(42) {
		t.Fatalf("before reset: %#v, %v", got, err)
	}

	if err := in.Reset(); err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Call(20, 22); !errors.Is(err, ErrStale) {
		t.Errorf("stale Func: %v, want ErrStale", err)
	}

	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fn.Call(20, 22); !errors.Is(err, ErrClosed) {
		t.Errorf("Func after Close: %v, want ErrClosed", err)
	}
}
