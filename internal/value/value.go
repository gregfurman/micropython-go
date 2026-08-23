// Package value holds the Go side of the boundary's value model: the types a
// Python value arrives as, and the coercion from one of those to whatever a
// host function asked for.
//
// It does the coercion with a type switch and nothing else. Reflection would
// take fewer lines and accept more types -- any slice, any map, any struct --
// but it moves the checking to run time and puts a reflect.Value between every
// argument and the function that wanted it. The types below are the ones the
// decoder actually produces; anything richer is the host function's own
// business, converted inside it where the compiler can still see it.
package value

import (
	"fmt"
	"math"
)

// Tuple is a Python tuple. It is distinct from []any so that the round trip
// back into Python can preserve tuple-ness, which JSON could not.
type Tuple []any

// Set is a Python set. Go has no set type, so it arrives as a slice -- but a
// distinct one, so a round trip back into Python stays a set rather than
// becoming a list, and so a reader is not misled into depending on the order.
// A set has none: these are the elements in whatever order the guest's hash
// table held them.
type Set []any

// FrozenSet is a Python frozenset. It is separate from Set only so the round
// trip preserves which one it was; nothing about it is immutable on this side.
type FrozenSet []any

// Unpack assigns src to whatever dst points at, converting where Python and Go
// disagree but Go itself would not.
//
// It switches on the target, then on the source. Both switches are over
// concrete types, so every destination type is checked where it is written and
// a call costs a pair of interface comparisons. It is the primitive the whole
// binding is built from -- starlark-go's unpackOneArg is the same function.
func Unpack(src any, dst any) error {
	switch p := dst.(type) {
	case nil:
		return fmt.Errorf("cannot unpack into a nil destination")

	case *any:
		*p = src
		return nil

	case *bool:
		if v, ok := src.(bool); ok {
			*p = v
			return nil
		}

	case *int:
		return toSigned(src, p, math.MinInt, math.MaxInt)
	case *int8:
		return toSigned(src, p, math.MinInt8, math.MaxInt8)
	case *int16:
		return toSigned(src, p, math.MinInt16, math.MaxInt16)
	case *int32:
		return toSigned(src, p, math.MinInt32, math.MaxInt32)
	case *int64:
		return toSigned(src, p, math.MinInt64, math.MaxInt64)

	case *uint:
		return toUnsigned(src, p, math.MaxUint)
	case *uint8:
		return toUnsigned(src, p, math.MaxUint8)
	case *uint16:
		return toUnsigned(src, p, math.MaxUint16)
	case *uint32:
		return toUnsigned(src, p, math.MaxUint32)
	case *uint64:
		return toUnsigned(src, p, math.MaxUint64)

	// An int widens to a float, as it would passing one to a Go float
	// parameter. A float never narrows to an int: that would drop part of the
	// value without saying so.
	case *float32:
		switch v := src.(type) {
		case int64:
			*p = float32(v)
			return nil
		case float64:
			*p = float32(v)
			return nil
		}
	case *float64:
		switch v := src.(type) {
		case int64:
			*p = float64(v)
			return nil
		case float64:
			*p = v
			return nil
		}

	case *string:
		switch v := src.(type) {
		case string:
			*p = v
			return nil
		case []byte:
			*p = string(v)
			return nil
		}

	case *[]byte:
		if v, ok := src.([]byte); ok {
			*p = v
			return nil
		}

	case *map[string]any:
		if v, ok := src.(map[string]any); ok {
			*p = v
			return nil
		}

	case *map[any]any:
		if v, ok := src.(map[any]any); ok {
			*p = v
			return nil
		}

	case *[]any:
		if v, ok := items(src); ok {
			*p = v
			return nil
		}
	case *Tuple:
		if v, ok := items(src); ok {
			*p = Tuple(v)
			return nil
		}
	case *Set:
		if v, ok := items(src); ok {
			*p = Set(v)
			return nil
		}
	case *FrozenSet:
		if v, ok := items(src); ok {
			*p = FrozenSet(v)
			return nil
		}
	}

	return fmt.Errorf("cannot use %s as %T", Name(src), dst)
}

// items reads anything the guest sends as a sequence. The four are the same
// slice underneath, and which one it was is a fact about the guest's type
// rather than about the elements.
func items(src any) ([]any, bool) {
	switch v := src.(type) {
	case []any:
		return v, true
	case Tuple:
		return v, true
	case Set:
		return v, true
	case FrozenSet:
		return v, true
	}
	return nil, false
}

func toSigned[T ~int | ~int8 | ~int16 | ~int32 | ~int64](src any, dst *T, lo, hi int64) error {
	v, ok := src.(int64)
	if !ok {
		return fmt.Errorf("cannot use %s as %T", Name(src), *dst)
	}
	if v < lo || v > hi {
		return fmt.Errorf("%d does not fit in %T", v, *dst)
	}
	*dst = T(v)
	return nil
}

func toUnsigned[T ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64](src any, dst *T, hi uint64) error {
	v, ok := src.(int64)
	if !ok {
		return fmt.Errorf("cannot use %s as %T", Name(src), *dst)
	}
	if v < 0 || uint64(v) > hi {
		return fmt.Errorf("%d does not fit in %T", v, *dst)
	}
	*dst = T(v)
	return nil
}

// Name names a value the way the guest would, so an error reads in the
// language the call was written in.
func Name(v any) string {
	switch value := v.(type) {
	case nil:
		return "None"
	case bool:
		return "bool"
	case int64:
		return "int"
	case float64:
		return "float"
	case string:
		return "str"
	case []byte:
		return "bytes"
	case []any:
		return "list"
	case Tuple:
		return "tuple"
	case Set:
		return "set"
	case FrozenSet:
		return "frozenset"
	case map[string]any, map[any]any:
		return "dict"
	case fmt.Stringer:
		return value.String()
	}
	return fmt.Sprintf("%T", v)
}
