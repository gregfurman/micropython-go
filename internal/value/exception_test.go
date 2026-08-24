package value

import (
	"errors"
	"testing"
)

func TestException_ErrorFormatting(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		msg  string
		want string
	}{
		{
			name: "both type and message",
			typ:  "ValueError",
			msg:  "invalid literal",
			want: "ValueError: invalid literal",
		},
		{
			name: "type only",
			typ:  "SyntaxError",
			msg:  "",
			want: "SyntaxError",
		},
		{
			name: "missing type falls back to UnknownError",
			typ:  "",
			msg:  "something went wrong",
			want: "UnknownError",
		},
		{
			name: "completely empty",
			typ:  "",
			msg:  "",
			want: "UnknownError",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewException(tt.typ, tt.msg)
			if got := e.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestException_Unwrap(t *testing.T) {
	t.Run("interrupted yields ErrInterrupted", func(t *testing.T) {
		e := &Exception{interrupted: true}

		// The standard library errors.Is will call Unwrap() automatically
		if !errors.Is(e, ErrInterrupted) {
			t.Errorf("expected errors.Is(e, ErrInterrupted) to be true")
		}
	})

	t.Run("not interrupted yields nil", func(t *testing.T) {
		e := &Exception{interrupted: false}

		if errors.Is(e, ErrInterrupted) {
			t.Errorf("did not expect error to match ErrInterrupted")
		}
		if unwrapped := e.Unwrap(); unwrapped != nil {
			t.Errorf("expected Unwrap() to return nil, got %v", unwrapped)
		}
	})
}

func TestException_FromGuest(t *testing.T) {
	t.Run("with streamed exception", func(t *testing.T) {
		streamed := NewException("KeyError", "missing key")
		rawTraceback := "Traceback (most recent call last):..."

		e := FromGuest(rawTraceback, streamed, true)

		if e.Type() != "KeyError" {
			t.Errorf("Type() = %q, want %q", e.Type(), "KeyError")
		}
		if e.Message() != "missing key" {
			t.Errorf("Message() = %q, want %q", e.Message(), "missing key")
		}
		if e.Raw() != rawTraceback {
			t.Errorf("Raw() = %q, want %q", e.Raw(), rawTraceback)
		}
		if !errors.Is(e, ErrInterrupted) {
			t.Error("expected exception to be marked as interrupted")
		}
	})

	t.Run("with nil streamed data", func(t *testing.T) {
		rawTraceback := "SyntaxError: invalid syntax"

		// Should handle nil gracefully by creating an empty Exception struct
		e := FromGuest(rawTraceback, nil, false)

		if e.Type() != "" {
			t.Errorf("expected empty Type, got %q", e.Type())
		}
		if e.Raw() != rawTraceback {
			t.Errorf("Raw() = %q, want %q", e.Raw(), rawTraceback)
		}
		if errors.Is(e, ErrInterrupted) {
			t.Error("did not expect exception to be marked as interrupted")
		}
	})
}

func TestException_Lift(t *testing.T) {
	e := NewException("TypeError", "bad type")

	lifted := e.lift()
	err, ok := lifted.(error)
	if !ok {
		t.Fatalf("lift() returned %T, want error interface", lifted)
	}

	if err.Error() != "TypeError: bad type" {
		t.Errorf("lifted error message = %q, want %q", err.Error(), "TypeError: bad type")
	}
}
