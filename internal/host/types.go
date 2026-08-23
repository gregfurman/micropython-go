package host

import "github.com/gregfurman/micropython-wasi/internal/value"

// The containers Go has no type for. Declared in internal/value alongside the
// values that Lift into them, so there is one Tuple rather than two alike.
type (
	Tuple     = value.Tuple
	Set       = value.Set
	FrozenSet = value.FrozenSet
	Exception = value.Exception
)

// Object is a Python value with no Go equivalent — a function, a class, an
// arbitrary instance. Only its type and repr survive the crossing.
type Object struct {
	Type string
	Repr string
}

func (o Object) String() string { return o.Repr }
