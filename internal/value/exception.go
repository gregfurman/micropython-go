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

func (e *Exception) Type() string    { return e.typ }
func (e *Exception) Message() string { return e.msg }

func (e *Exception) lower(w Writer) {
	w.Tag(TagException)
	Str(e.typ).lower(w)
	Str(e.msg).lower(w)
}
func (e *Exception) lift() any { return error(e) }

// ------------------------------------------------------------------

func (e *Exception) Unwrap() error {
	if e.interrupted {
		return ErrInterrupted
	}
	return nil
}

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

func (e *Exception) Raw() string {
	return e.raw
}
