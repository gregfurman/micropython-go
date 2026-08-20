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
