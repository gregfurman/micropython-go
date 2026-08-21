package host

// Object is a Python value with no Go equivalent — a function, a class, an
// arbitrary instance. Only its type and repr survive the crossing.
type Object struct {
	Type string
	Repr string
}

func (o Object) String() string { return o.Repr }

// Tuple is a Python tuple. It is distinct from []any so that the round trip
// back into Python can preserve tuple-ness, which JSON could not.
type Tuple []any

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
