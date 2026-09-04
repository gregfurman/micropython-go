package micropython

import (
	"context"
	"fmt"
	"iter"
	"math/big"

	"github.com/gregfurman/micropython-go/internal/value"
	"golang.org/x/exp/constraints"
)

// Value is one Python value, either built here to send to the guest or handed
// back by Eval, Call and Get.
//
// Export converts it to the closest Go value; the As methods, one per Python
// type, convert it precisely and report a mismatch. The builders that make one
// live alongside the conversions for the same type: scalar_values.go,
// collection_values.go, callable_values.go, object_values.go and
// exception_values.go.
type Value struct {
	val value.Value
}

func wrapValue(v value.Value) Value {
	return Value{val: v}
}

// Type names the value as Python would, so an Object reports its class and an
// exception its exception class.
func (v Value) Type() string {
	if v.val == nil {
		return "invalid"
	}
	return v.val.Type()
}

// Export converts the value to the closest Go equivalent, following the Type
// Conversion table: int64, float64, string, []byte, []any, map[string]any and
// so on. Composite exports deliberately flatten to ordinary Go collections;
// use an As method when the exact Python container type matters.
func (v Value) Export() any {
	return value.Lift(v.val)
}

func (v Value) String() string {
	switch x := v.val.(type) {
	case nil:
		return "<invalid>"
	case value.None:
		return "None"
	case value.Bool:
		if x {
			return "True"
		}
		return "False"
	default:
		return fmt.Sprint(value.Lift(v.val))
	}
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
//	map[string]any, map[any]any             dict
//	anything else (e.g. structs)            JSON round-trip (dict/list)
func Of(v any) Value {
	if built, ok := v.(Value); ok {
		return built
	}

	x, err := value.Lower(v)
	if err != nil {
		return wrapValue(value.Invalid(err))
	}

	return wrapValue(x)
}

func conversionError(v Value, expected string) error {
	return fmt.Errorf(
		"micropython: expected Python %s, got %s",
		expected,
		v.Type(),
	)
}

// unwrapArgs lowers the host's own wrappers to the value model the codec
// encodes. A Func keeps the guest's handle, so it goes over as that function
// rather than as the Go closure around it.
func unwrapArgs(args []any) []any {
	out := make([]any, len(args))

	for i, arg := range args {
		switch arg := arg.(type) {
		case Value:
			out[i] = arg.val
		case Func:
			out[i] = arg.c
		case *Func:
			out[i] = arg.c
		case Iterator:
			out[i] = arg.i
		case *Iterator:
			out[i] = arg.i
		default:
			out[i] = arg
		}
	}

	return out
}

type Iterator struct {
	i  value.Object
	in *Instance
}

func (i Iterator) Iter(ctx context.Context) iter.Seq2[Value, error] {
	return func(yield func(Value, error) bool) {
		if i.i.Ref() == 0 || i.in == nil || i.in.wrapped == nil {
			yield(Value{}, ErrInstanceNotInitialised)
			return
		}

		for {
			out, more, err := i.in.wrapped.NextGenerator(ctx, i.i)
			if err != nil {
				yield(Value{}, err)
				return
			}
			if !more {
				return
			}
			if !yield(wrapValue(out), nil) {
				return
			}
		}
	}
}

func (v Value) IsIterator() bool {
	obj, ok := v.val.(value.Object)
	return ok && obj.IsIterable()
}

// AsIterator binds a guest iterator value to this Instance for lazy iteration.
func (i *Instance) AsIterator(v Value) (Iterator, error) {
	if i == nil || i.wrapped == nil {
		return Iterator{}, ErrInstanceNotInitialised
	}

	it, ok := v.val.(value.Object)
	if !ok || it.Ref() == 0 || !it.IsIterable() {
		return Iterator{}, conversionError(v, "iterator")
	}
	return Iterator{i: it, in: i}, nil
}

// ---------------------------------------------------------------------

// None returns a Python None value.
func None() Value {
	return wrapValue(value.None{})
}

// Bool converts a Go bool to a Python bool.
func Bool(b bool) Value {
	return wrapValue(value.Bool(b))
}

// Int converts a Go int64 to a Python int.
func Int(n int64) Value {
	return wrapValue(value.Int(n))
}

// BigInt converts a big.Int to a Python int, for the magnitudes int64 cannot
// hold. Python ints have no width limit.
func BigInt(n *big.Int) Value {
	return wrapValue(value.NewBigInt(n))
}

// Float converts a Go float64 to a Python float.
func Float(f float64) Value {
	return wrapValue(value.Float(f))
}

// Str converts a Go string to a Python str.
func Str(s string) Value {
	return wrapValue(value.Str(s))
}

// Bytes converts a Go byte slice to a Python bytes object.
func Bytes(b []byte) Value {
	out := make([]byte, len(b))
	copy(out, b)
	return wrapValue(value.Bytes(out))
}

// ---------------------------------------------------------------------

// IsNone reports whether the value is Python's None.
func (v Value) IsNone() bool {
	_, ok := v.val.(value.None)
	return ok
}

func (v Value) AsBool() (bool, error) {
	x, ok := v.val.(value.Bool)
	if !ok {
		return false, conversionError(v, "bool")
	}
	return bool(x), nil
}

// AsInt returns a Python int, whatever its magnitude on the guest, and reports
// an error rather than truncating one that does not fit. AsBigInt takes those.
func (v Value) AsInt() (int64, error) {
	switch x := v.val.(type) {
	case value.Int:
		return int64(x), nil

	case value.BigInt:
		n := x.Unwrap()
		if !n.IsInt64() {
			return 0, fmt.Errorf(
				"micropython: Python int %s overflows int64",
				n,
			)
		}
		return n.Int64(), nil

	default:
		return 0, conversionError(v, "int")
	}
}

func (v Value) AsBigInt() (*big.Int, error) {
	switch x := v.val.(type) {
	case value.Int:
		return big.NewInt(int64(x)), nil

	case value.BigInt:
		return x.Unwrap(), nil

	default:
		return nil, conversionError(v, "int")
	}
}

func (v Value) AsFloat() (float64, error) {
	x, ok := v.val.(value.Float)
	if !ok {
		return 0, conversionError(v, "float")
	}
	return float64(x), nil
}

func (v Value) AsString() (string, error) {
	x, ok := v.val.(value.Str)
	if !ok {
		return "", conversionError(v, "str")
	}
	return string(x), nil
}

func (v Value) AsBytes() ([]byte, error) {
	x, ok := v.val.(value.Bytes)
	if !ok {
		return nil, conversionError(v, "bytes")
	}

	out := make([]byte, len(x))
	copy(out, x)
	return out, nil
}

// ---------------------------------------------------------------------

// Item is one entry of a Dict.
type Item struct {
	Key Value
	Val Value
}

// List creates a Python list from the given values.
func List(items ...Value) Value {
	return wrapValue(value.NewList(unwrap(items)...))
}

// Tuple creates a Python tuple from the given values.
func Tuple(items ...Value) Value {
	return wrapValue(value.NewTuple(unwrap(items)...))
}

// Set creates a mutable Python set from the given values.
func Set(items ...Value) Value {
	return wrapValue(value.NewSet(unwrap(items)...))
}

// FrozenSet creates an immutable Python frozenset from the given values.
func FrozenSet(items ...Value) Value {
	return wrapValue(value.NewFrozenSet(unwrap(items)...))
}

// Dict creates a Python dictionary from the given key-value items.
func Dict(entries ...Item) Value {
	out := make([]value.Item, len(entries))

	for i, entry := range entries {
		out[i] = value.Item{
			Key: entry.Key.val,
			Val: entry.Val.val,
		}
	}

	return wrapValue(value.NewDict(out...))
}

// Strs is a convenience function that creates a Python list of strings.
func Strs(items ...string) Value {
	out := make([]Value, len(items))
	for i, item := range items {
		out[i] = Str(item)
	}
	return List(out...)
}

// Ints is a convenience function that creates a Python list of integers.
func Ints[T constraints.Integer](items []T) Value {
	out := make([]Value, len(items))
	for i, item := range items {
		out[i] = Of(item)
	}
	return List(out...)
}

// ---------------------------------------------------------------------

func (v Value) AsList() ([]Value, error) {
	x, ok := v.val.(value.ListValue)
	if !ok {
		return nil, conversionError(v, "list")
	}
	return wrapValues(x), nil
}

func (v Value) AsTuple() ([]Value, error) {
	x, ok := v.val.(value.TupleValue)
	if !ok {
		return nil, conversionError(v, "tuple")
	}
	return wrapValues(x), nil
}

func (v Value) AsSet() ([]Value, error) {
	x, ok := v.val.(value.SetValue)
	if !ok {
		return nil, conversionError(v, "set")
	}
	return wrapValues(x), nil
}

func (v Value) AsFrozenSet() ([]Value, error) {
	x, ok := v.val.(value.FrozenSetValue)
	if !ok {
		return nil, conversionError(v, "frozenset")
	}
	return wrapValues(x), nil
}

// AsDict returns the pairs of a Python dict, in the order the guest sent them.
// Export gives a Go map instead, which drops that order and stringifies any key
// a Go map cannot hold.
func (v Value) AsDict() ([]Item, error) {
	entries, ok := v.val.(value.DictValue)
	if !ok {
		return nil, conversionError(v, "dict")
	}

	out := make([]Item, len(entries))
	for i, entry := range entries {
		out[i] = Item{
			Key: wrapValue(entry.Key),
			Val: wrapValue(entry.Val),
		}
	}

	return out, nil
}

// ---------------------------------------------------------------------

func unwrap(items []Value) []value.Value {
	out := make([]value.Value, len(items))
	for i, v := range items {
		out[i] = v.val
	}
	return out
}

func wrapValues[S ~[]value.Value](items S) []Value {
	out := make([]Value, len(items))
	for i, item := range items {
		out[i] = wrapValue(item)
	}
	return out
}

// ---------------------------------------------------------------------

// Callable is the guest's handle on a Python function. It carries no
// interpreter of its own, so it is the form that can be passed back to Python
// as an argument; Value.AsCallable takes one apart and Instance.AsCallable
// binds it to an interpreter.

// Func is a Callable bound to the Instance that produced it. Call invokes it,
// and passing one to Call, Set or a HostFunc's result lowers it back to the
// guest's own function object, the same as passing the Value it came from.
type Func struct {
	c  Object
	in *Instance
}

// Call invokes the function. It queues on the Instance the same way Eval and
// Call do, so it cannot be invoked from inside a HostFunc, which is already
// holding that Instance.
func (f Func) Call(ctx context.Context, args ...any) (Value, error) {
	if f.c.Ref() == 0 || f.in == nil || f.in.wrapped == nil {
		return Value{}, ErrInstanceNotInitialised
	}

	out, err := f.in.wrapped.CallRef(ctx, f.c, unwrapArgs(args))
	if err != nil {
		return Value{}, err
	}

	return wrapValue(out), nil
}

// Value returns the function as a Value, the form Set and Globals take.
func (f Func) Value() Value { return wrapValue(f.c) }

// IsCallable reports whether Python's callable() accepts this value.
// Instance.AsCallable turns one into a Go function.
func (v Value) IsCallable() bool {
	obj, ok := v.val.(value.Object)
	return ok && obj.IsCallable()
}

func (v Value) AsCallable() (func(
	ctx context.Context,
	instance *Instance,
	args ...any,
) (Value, error), error) {
	c, ok := v.val.(value.Object)
	if !ok || c.Ref() == 0 {
		return nil, conversionError(v, "callable")
	}

	return func(
		ctx context.Context,
		instance *Instance,
		args ...any,
	) (Value, error) {
		out, err := instance.wrapped.CallRef(
			ctx,
			c,
			unwrapArgs(args),
		)
		if err != nil {
			return Value{}, err
		}

		return wrapValue(out), nil
	}, nil
}

// AsCallable binds a Python callable to this Instance. It reaches the host as
// an opaque handle rather than a Go value, so this is the way to invoke one:
//
//	got, err := in.Eval(ctx, "lambda x: x * 2")
//	double, err := in.AsCallable(got)
//	out, err := double.Call(ctx, 21) // 42
//
// Anything Python's callable() accepts qualifies, including bound methods,
// classes, and instances defining __call__. The result can also be passed
// straight back to Python, where it arrives as the same function object.
func (i *Instance) AsCallable(v Value) (Func, error) {
	if i == nil || i.wrapped == nil {
		return Func{}, ErrInstanceNotInitialised
	}

	c, ok := v.val.(value.Object)
	if !ok || c.Ref() == 0 || !c.IsCallable() {
		return Func{}, conversionError(v, "callable")
	}

	return Func{c: c, in: i}, nil
}

// ---------------------------------------------------------------------

// Object is a Python value with no Go equivalent, such as a class or an
// arbitrary instance, where only the type name and the repr cross over. It
// cannot be passed back to the guest.
type Object = value.Object

func (v Value) AsObject() (Object, error) {
	x, ok := v.val.(value.Object)
	if !ok {
		return Object{}, conversionError(v, "object")
	}
	return x, nil
}

// PythonError is the error a failing call returns. Unwrap it to read which
// exception the guest raised, rather than matching on the message:
//
//	var exc *micropython.PythonError
//	if errors.As(err, &exc) && exc.Type() == "KeyError" { ... }
//
// Exception, by contrast, builds one to send.
type PythonError = value.Exception

// Exception builds a Python exception as a Value, for binding one the guest can
// raise itself:
//
//	WithGlobals(Globals{"BAD": Exception("ValueError", "bad input")})
//
// Returning one as a HostFunc's result raises it at the call site, which is what
// Raise does in the error position.
func Exception(typ, msg string) Value {
	return wrapValue(value.NewException(typ, msg))
}

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
func Raise(typ, msg string) error {
	return value.NewException(typ, msg)
}
