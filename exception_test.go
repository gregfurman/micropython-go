package micropython

import (
	_ "embed"
	"errors"
	"strings"
	"testing"
)

//go:embed testdata/exceptions.py
var exceptionsFile string

func TestExceptions(t *testing.T) {
	instance := newT(t)

	if _, err := instance.Exec(t.Context(), exceptionsFile); err != nil {
		t.Fatalf("unexpected error on Exec: %s", err)
	}

	tests := []struct {
		name string
		fn   string
		typ  string
		msg  string
	}{
		{
			name: "raises base exception",
			fn:   "raises_exception",
			typ:  "Exception",
		},
		{
			name: "raises base exception with args",
			fn:   "raises_exception_with_args",
			typ:  "Exception",
			msg:  "this is an exception",
		},
		{
			// The fixture routes its message to the `custom` attribute and
			// passes nothing on to super(), so the exception itself carries no
			// message -- only the type name survives.
			name: "raises custom exception",
			fn:   "raises_custom_exception",
			typ:  "CustomException",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := instance.Call(t.Context(), tt.fn)

			var exc *Exception
			if !errors.As(err, &exc) {
				t.Fatalf("%s() returned %v (%T), want *Exception", tt.fn, err, err)
			}
			if exc.Type != tt.typ {
				t.Errorf("Type = %q, want %q", exc.Type, tt.typ)
			}
			if exc.Message != tt.msg {
				t.Errorf("Message = %q, want %q", exc.Message, tt.msg)
			}

			// Error() is the type and message alone. The frame the exception
			// came from is only in Raw.
			if !strings.Contains(exc.Error(), tt.typ) {
				t.Errorf("Error() does not mention %q: %s", tt.typ, exc)
			}
			if !strings.Contains(exc.Raw(), tt.fn) {
				t.Errorf("traceback does not mention %q:\n%s", tt.fn, exc.Raw())
			}
		})
	}

	t.Run("custom attribute survives", func(t *testing.T) {
		got, err := instance.Call(t.Context(), "raises_exception_formatted")
		if err != nil {
			t.Fatal(err)
		}
		if want := "Exception: Exception: this is an exception"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("interpreter survives", func(t *testing.T) {
		got, err := instance.Eval(t.Context(), "1 + 1")
		if err != nil {
			t.Fatal(err)
		}
		if got != int64(2) {
			t.Errorf("1 + 1 = %#v", got)
		}
	})
}

// Text is the traceback as MicroPython printed it, untouched: every frame in
// call order with the line it was on. It is the only place that detail
// survives, since Exception does not break the traceback into fields.
func TestText(t *testing.T) {
	instance := newT(t)

	if _, err := instance.Exec(t.Context(), "def outer():\n    inner()\n\ndef inner():\n    raise ValueError('boom')\n"); err != nil {
		t.Fatal(err)
	}

	var exc *Exception
	if _, err := instance.Call(t.Context(), "outer"); !errors.As(err, &exc) {
		t.Fatalf("got %v (%T), want *Exception", err, err)
	}

	want := "Traceback (most recent call last):\n" +
		"  File \"<string>\", line 2, in outer\n" +
		"  File \"<string>\", line 5, in inner\n" +
		"ValueError: boom\n"
	if exc.Raw() != want {
		t.Errorf("Raw() =\n%q\nwant\n%q", exc.Raw(), want)
	}

	// Error() reports the type and message alone, so it reads correctly when a
	// caller wraps it in a sentence; Raw is what you print to debug.
	if got, want := exc.Error(), "ValueError: boom"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// Eval parses in expression context and Exec in statement context, so `raise`
// is a SyntaxError through the first and an Exception through the second.
// CPython draws the same line: eval("raise ValueError()") does not compile.
func TestRaiseNeedsStatementContext(t *testing.T) {
	instance := newT(t)

	if _, err := instance.Eval(t.Context(), "raise Exception()"); err == nil {
		t.Error("Eval accepted a statement")
	} else if !strings.Contains(err.Error(), "SyntaxError") {
		t.Errorf("got %q, want SyntaxError", err)
	}

	if _, err := instance.Exec(t.Context(), "raise Exception()"); err == nil {
		t.Error("Exec swallowed the exception")
	} else if strings.Contains(err.Error(), "SyntaxError") {
		t.Errorf("Exec reported a syntax error: %s", err)
	}

	// An expression that raises is fine through Eval; it is the statement that
	// is not.
	if _, err := instance.Eval(t.Context(), "1/0"); err == nil {
		t.Error("Eval swallowed ZeroDivisionError")
	}
}
