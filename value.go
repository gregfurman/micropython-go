package micropython

import (
	"github.com/gregfurman/micropython-wasi/internal/host"
	"github.com/gregfurman/micropython-wasi/internal/value"
)

// Building the Python values a host can bind.
//
// The set is closed: WithGlobals takes a Value and nothing else, so a Go type
// with no Python equivalent is a compile error rather than something the
// encoder has to find an answer for.
//
//	micropython.WithGlobals(micropython.Globals{
//	    "NAME":   micropython.Str("service"),
//	    "LIMITS": micropython.Dict(micropython.Item{Key: micropython.Str("retries"), Val: micropython.Int(3)}),
//	    "TAGS":   micropython.Strs("a", "b"),
//	})
//
// Each value knows both directions: Lower writes it in the format the guest
// decodes, Lift returns the plain Go value a caller would have seen had it come
// the other way.
type (

	// The names below are types rather than constructors because that is the
	// half a caller cannot work around: Exception is what errors.As needs to
	// inspect a failure, and Tuple, Set and FrozenSet are what a type switch
	// on a result matches. Building one is New*.
	// Exception = value.Exception
	// Tuple     = value.Tuple
	// Set       = value.Set
	// FrozenSet = value.FrozenSet

	// Object is a Python value with no Go equivalent -- a function, a class,
	// an instance. Only its type and repr survive the crossing.
	Object = host.Object
)

// Value is a Python value the host has built. It wraps the internal one so
// this package's surface names nothing a caller cannot import.
type Value struct {
	val value.Value
}

// Type names the value as Python would.
func (v Value) Type() string { return v.val.Type() }

// lift is the plain Go value this stands for, which is what a call returns for
// the same Python value coming the other way. Unexported: a caller building a
// value has no use for it, and it would put the boundary's vocabulary on a
// public type.
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

var ErrNoCallable = value.ErrNoCallable

func None() Value           { return Value{val: value.None{}} }
func Bool(b bool) Value     { return Value{val: value.Bool(b)} }
func Int(n int64) Value     { return Value{val: value.Int(n)} }
func Float(f float64) Value { return Value{val: value.Float(f)} }
func Str(s string) Value    { return Value{val: value.Str(s)} }
func Bytes(b []byte) Value  { return Value{val: value.Bytes(b)} }

func List(items ...Value) Value { return Value{val: value.NewList(unwrap(items)...)} }

func Dict(entries ...Item) Value {
	out := make([]value.Item, len(entries))
	for i, e := range entries {
		out[i] = value.Item{Key: e.Key.val, Val: e.Val.val}
	}
	return Value{val: value.NewDict(out...)}
}

// New* where the plain name is the type above.

// func NewException(typ, msg string) Value { return Value{val: value.NewException(typ, msg)} }

// Raise builds an exception as a Value, for binding one the guest can raise.
func Exception(typ, msg string) Value { return Value{val: value.NewException(typ, msg)} }
func Tuple(items ...Value) Value      { return Value{val: value.NewTuple(unwrap(items)...)} }
func Set(items ...Value) Value        { return Value{val: value.NewSet(unwrap(items)...)} }
func FrozenSet(items ...Value) Value  { return Value{val: value.NewFrozenSet(unwrap(items)...)} }

func Strs(items ...string) Value {
	out := make([]Value, len(items))
	for i, s := range items {
		out[i] = Str(s)
	}
	return List(out...)
}

func Ints(items ...int64) Value {
	out := make([]Value, len(items))
	for i, n := range items {
		out[i] = Int(n)
	}
	return List(out...)
}

// Of converts an ordinary Go value to the Python one it stands for, for
// passing data you already have rather than building it piece by piece.
//
//	p.Call(ctx, "handle", micropython.Of(row))
//
// The rules are Go type to the obvious Python type: a slice becomes a list, a
// map or struct becomes a dict, and anything unrecognised goes through JSON.
// Where that is not what you meant -- a tuple and a list are both slices in Go
// -- build the value instead.
//
// A value it cannot convert is reported when the call uses it, so this can be
// written inline.
func Of(v any) Value {
	if built, ok := v.(Value); ok {
		return built
	}

	x, err := host.ToValue(v)
	if err != nil {
		return Value{val: value.Invalid(err)}
	}
	return Value{val: x}
}

func unwrapArgs(args []Value) []any {
	out := make([]any, len(args))
	for i, v := range args {
		out[i] = v.val
	}
	return out
}
