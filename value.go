package micropython

import (
	"github.com/gregfurman/micropython-go/internal/value"
	"golang.org/x/exp/constraints"
)

// Value is a Python value the host has built. It wraps the internal one so
// this package's surface names nothing a caller cannot import.
type Value struct {
	val value.Value
}

// Type names the value as Python would.
func (v Value) Type() string { return v.val.Type() }

func (v Value) lift() any { return value.Lift(v.val) }

// Item is one entry of a Dict.
type Item struct {
	Key Value
	Val Value
}

func unwrap(items []Value) []value.Value {
	out := make([]value.Value, len(items))
	for i, v := range items {
		out[i] = v.val
	}
	return out
}

// None returns a Python None value.
func None() Value { return Value{val: value.None{}} }

// Bool converts a Go bool to a Python bool.
func Bool(b bool) Value { return Value{val: value.Bool(b)} }

// Int converts a Go int64 to a Python int.
func Int(n int64) Value { return Value{val: value.Int(n)} }

// Float converts a Go float64 to a Python float.
func Float(f float64) Value { return Value{val: value.Float(f)} }

// Str converts a Go string to a Python str.
func Str(s string) Value { return Value{val: value.Str(s)} }

// Bytes converts a Go byte slice to a Python bytes object.
func Bytes(b []byte) Value { return Value{val: value.Bytes(b)} }

// List creates a Python list from the given values.
func List(items ...Value) Value { return Value{val: value.NewList(unwrap(items)...)} }

// Dict creates a Python dictionary from the given key-value items.
func Dict(entries ...Item) Value {
	out := make([]value.Item, len(entries))
	for i, e := range entries {
		out[i] = value.Item{Key: e.Key.val, Val: e.Val.val}
	}
	return Value{val: value.NewDict(out...)}
}

// PythonError is the error a failing call returns. Unwrap it to read which
// exception the guest raised, rather than matching on the message:
//
//	var exc *micropython.PythonError
//	if errors.As(err, &exc) && exc.Type() == "KeyError" { ... }
//
// Exception, by contrast, builds one to send.
type PythonError = value.Exception

// Exception builds a Python exception as a Value, for binding one the guest can raise.
func Exception(typ, msg string) Value { return Value{val: value.NewException(typ, msg)} }

// Raise returns an error that makes the guest raise a specific Python exception.
// Return it from a HostFunc to control which class the caller sees:
//
//	in.DefineFunction(ctx, "lookup", func(args []any) (any, error) {
//	    return nil, micropython.Raise("KeyError", "missing")
//	})
//
// Python then catches it as a KeyError. A typ that does not name a builtin
// exception falls back to HostError, as does any other error a HostFunc
// returns, carrying that error's text as the message.
func Raise(typ, msg string) error { return value.NewException(typ, msg) }

// Tuple creates a Python tuple from the given values.
func Tuple(items ...Value) Value { return Value{val: value.NewTuple(unwrap(items)...)} }

// Set creates a mutable Python set from the given values.
func Set(items ...Value) Value { return Value{val: value.NewSet(unwrap(items)...)} }

// FrozenSet creates an immutable Python frozenset from the given values.
func FrozenSet(items ...Value) Value { return Value{val: value.NewFrozenSet(unwrap(items)...)} }

// Strs is a convenience function that creates a Python list of strings.
func Strs(items ...string) Value {
	out := make([]Value, len(items))
	for i, s := range items {
		out[i] = Str(s)
	}
	return List(out...)
}

// Ints is a convenience function that creates a Python list of integers.
func Ints[T constraints.Integer](items []T) Value {
	out := make([]Value, len(items))
	for i, n := range items {
		out[i] = Int(int64(n))
	}
	return List(out...)
}

// Of converts an ordinary Go value to its closest resembling Python representation:
//
//	Go value                                Python representation
//	-------------------------------------------------------------
//	nil                                     None
//	bool                                    bool
//	int, int64, other integers              int
//	float32, float64                        float
//	string                                  str
//	[]byte                                  bytes
//	[]any                                   list
//	Tuple                                   tuple
//	Set, FrozenSet                          set, frozenset
//	map[string]any, map[any]any             dict
//	anything else (e.g. structs)            JSON round-trip (dict/list)
func Of(v any) Value {
	if built, ok := v.(Value); ok {
		return built
	}

	x, err := value.FromGo(v)
	if err != nil {
		return Value{val: value.Invalid(err)}
	}
	return Value{val: x}
}

func unwrapArgs(args []any) []any {
	for i, a := range args {
		if v, ok := a.(Value); ok {
			args[i] = v.val
		}
	}
	return args
}
