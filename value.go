package micropython

import (
	"github.com/gregfurman/micropython-go/internal/host"
	"github.com/gregfurman/micropython-go/internal/value"
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

// Of converts an ordinary Go value to its closest resembling Python representation:
//
// Go value                                Python representation
// -------------------------------------------------------------
// nil                                     None
// bool                                    bool
// int, int64, other integers              int
// float32, float64                        float
// string                                  str
// []byte                                  bytes
// []any                                   list
// Tuple                                   tuple
// Set, FrozenSet                          set, frozenset
// map[string]any, map[any]any             dict
// anything else (e.g. structs)            JSON round-trip (dict/list)
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

func unwrapArgs(args []any) []any {
	for i, a := range args {
		if v, ok := a.(Value); ok {
			args[i] = v.val
		}
	}
	return args
}
