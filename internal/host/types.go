package host

import "github.com/gregfurman/micropython-wasi/internal/value"

// Object is a Python value with no Go equivalent — a function, a class, an
// arbitrary instance.
type Object struct {
	Type string
	Repr string

	callable bool
	abi      *ABI
	ref      int32
	epoch    uint64
}

func (o Object) String() string { return o.Repr }

// Callable reports whether Call will work.
func (o Object) Callable() bool { return o.callable }

// The containers Go has no type for. They are defined in internal/value,
// because that is where the coercion switches on them, and aliased here so
// there is one set of types rather than two that look alike.
type (
	Tuple     = value.Tuple
	Set       = value.Set
	FrozenSet = value.FrozenSet
)

// Exception is a Python exception that reached the host. It is the error every
// failing Eval, Exec and Call returns.
type Exception struct {
	// Type is the exception's class name, such as "ValueError".
	//
	// This member is required.
	Type string

	// Message is str(exc), which for the usual single-argument exception is
	// that argument as text. Empty for an exception raised with no arguments.
	Message string

	// raw is the traceback exactly as MicroPython printed it, trailing
	// newline included, and is what Error reports. Empty for the errors the
	// module never raised, such as a name that did not resolve.
	raw string

	interrupted bool
}

func (e *Exception) Error() string {
	if e.Type == "" {
		// should always have a type
		return "UnknownError"
	}

	if e.Message == "" {
		return e.Type
	}

	return e.Type + ": " + e.Message
}

func (e *Exception) Raw() string {
	return e.raw
}

// Unwrap reports ErrInterrupted for the exception a cancellation caused, so
// callers can tell a guest that stopped from a guest that failed.
func (e *Exception) Unwrap() error {
	if e.interrupted {
		return ErrInterrupted
	}
	return nil
}

// fill reads the (type, message) tuple mp_api_emit_error streamed. A shape it
// does not recognise is dropped rather than reported: the exception still has
// its text, which is the part a caller reads.
func (e *Exception) fill(v any) {
	parts, ok := v.(Tuple)
	if !ok || len(parts) != 2 {
		return
	}
	e.Type, _ = parts[0].(string)
	e.Message, _ = parts[1].(string)
}
