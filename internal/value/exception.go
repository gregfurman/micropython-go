package value

import "errors"

var (
	ErrInterrupted = errors.New("micropython: call was interrupted")
)

type Exception struct {
	typ         string
	msg         string
	raw         string
	interrupted bool
}

func NewException(typ, msg string) *Exception {
	return &Exception{typ: typ, msg: msg}
}

func FromGuest(raw string, streamed any, interrupted bool) *Exception {
	e, _ := streamed.(*Exception)
	if e == nil {
		e = &Exception{}
	}
	e.raw, e.interrupted = raw, interrupted
	return e
}

// Type is the exception's class name, such as "ValueError".
func (e *Exception) Type() string { return e.typ }

// Message is str(exc): for the usual single-argument exception, that argument
// as text. Empty for one raised with no arguments.
func (e *Exception) Message() string { return e.msg }

func (e *Exception) lift() any { return error(e) }

// ------------------------------------------------------------------

func (e *Exception) Unwrap() error {
	if e.interrupted {
		return ErrInterrupted
	}
	return nil
}

// Error reports the exception as Python writes it, "Type: message".
func (e *Exception) Error() string {
	if e.typ == "" {
		// should always have a type
		return "UnknownError"
	}

	if e.msg == "" {
		return e.typ
	}

	return e.typ + ": " + e.msg
}

// Raw is the traceback exactly as MicroPython printed it, trailing newline
// included. Empty for errors the module never raised, such as a name that did
// not resolve.
func (e *Exception) Raw() string {
	return e.raw
}
