package micropython

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math/big"

	"github.com/gregfurman/micropython-go/internal/value"
)

var errZeroValue = errors.New("micropython: the zero Value holds nothing to convert")

// Value represents copied Python data or a handle owned by an interpreter.
// Use Export for Go data or the As methods for checked, type-specific access.
// The zero Value is invalid; use [None] for Python None.
type Value struct {
	val value.Value
}

func wrapValue(v value.Value) Value {
	return Value{val: v}
}

// Type returns the Python type name, or "invalid" for a zero Value.
func (v Value) Type() string {
	if v.val == nil {
		return "invalid"
	}
	return v.val.Type()
}

// Export converts data to Go scalars, slices, and maps. Handles remain Values,
// including inside containers. None and the zero Value export as nil.
// Container kinds are flattened and non-comparable dictionary keys stringified;
// use the As methods when those distinctions matter.
func (v Value) Export() any {
	if v.val == nil {
		return nil
	}
	return exported(value.Lift(v.val))
}

func exported(v any) any {
	switch x := v.(type) {
	case value.Object:
		return wrapValue(x)

	case []any:
		return exportedSlice(x)
	case value.Tuple:
		return value.Tuple(exportedSlice(x))
	case value.Set:
		return value.Set(exportedSlice(x))
	case value.FrozenSet:
		return value.FrozenSet(exportedSlice(x))

	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = exported(item)
		}
		return out
	case map[any]any:
		out := make(map[any]any, len(x))
		for k, item := range x {
			out[exported(k)] = exported(item)
		}
		return out

	default:
		return v
	}
}

func exportedSlice(items []any) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = exported(item)
	}
	return out
}

// String formats v for display; it is not Python repr or serialization.
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

// Of converts Go data to a Value. Nil becomes None; []byte becomes bytes.
// Scalars and collections use native conversions; other types use JSON.
// Use builders such as [Tuple] when the Python type matters.
// Conversion failures produce an invalid Value, reported when passed to Python.
func Of(v any) Value {
	if built, ok := v.(Value); ok {
		return built
	}

	x, err := value.Lower(unwrapAny(v))
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

func unwrapArgs(args []any) []any {
	out := make([]any, len(args))
	for i, arg := range args {
		out[i] = unwrapAny(arg)
	}
	return out
}

func unwrapAny(v any) any {
	switch x := v.(type) {
	case Value:
		if x.val == nil {
			// A Value that was never built carries no intent. Sending None in
			// its place would hide the mistake where it matters least, so it
			// converts to a refusal the encoder reports instead.
			return value.Invalid(errZeroValue)
		}
		return x.val
	case Object:
		return x.unwrap()
	case Func:
		return x.c.unwrap()
	case *Func:
		return x.c.unwrap()
	case Iterator:
		return x.i
	case *Iterator:
		return x.i

	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = unwrapAny(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = unwrapAny(item)
		}
		return out
	case map[any]any:
		out := make(map[any]any, len(x))
		for k, item := range x {
			out[unwrapAny(k)] = unwrapAny(item)
		}
		return out

	default:
		return v
	}
}

// Iterator is a guest iterator bound to an Instance by [Instance.AsIterator].
type Iterator struct {
	i  value.Object
	in *Instance
}

// Iter yields values until exhaustion or the first error.
// Stopping early leaves the iterator positioned for a later call to Iter.
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

// IsIterator reports whether v holds a guest iterator, not a copied collection.
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

// BigInt copies n into a Python int Value; nil becomes zero.
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

// Bytes copies b into a Python bytes Value.
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

// AsInt returns an int64, reporting type mismatch or overflow.
// Use [Value.AsBigInt] for larger integers.
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

// AsDict returns dictionary entries in received order without converting keys.
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

// Func is a Python callable bound by [Instance.AsCallable].
// It can be invoked in Go or passed back to the same interpreter.
type Func struct {
	c  Object
	in *Instance
}

// Call invokes the bound function with [Instance.Call] argument conversions.
// It must not be called from a HostFunc running on the same Instance.
func (f Func) Call(ctx context.Context, args ...any) (Value, error) {
	if f.c.ref() == 0 || f.in == nil || f.in.wrapped == nil {
		return Value{}, ErrInstanceNotInitialised
	}

	out, err := f.in.wrapped.CallRef(ctx, f.c.unwrap(), unwrapArgs(args))
	if err != nil {
		return Value{}, err
	}

	return wrapValue(out), nil
}

// Value returns the function's handle as a Value.
func (f Func) Value() Value {
	return wrapValue(f.c.unwrap())
}

// IsCallable reports whether v holds a Python callable.
func (v Value) IsCallable() bool {
	obj, ok := v.val.(value.Object)
	return ok && obj.IsCallable()
}

// AsCallable binds a callable Value to this Instance for invocation through [Func.Call].
// The value must belong to this interpreter and remain live when called.
func (i *Instance) AsCallable(v Value) (Func, error) {
	if i == nil || i.wrapped == nil {
		return Func{}, ErrInstanceNotInitialised
	}

	c, ok := v.val.(value.Object)
	if !ok || c.Ref() == 0 || !c.IsCallable() {
		return Func{}, conversionError(v, "callable")
	}

	return Func{c: Object{obj: c}, in: i}, nil
}

// ---------------------------------------------------------------------

// Object is an opaque guest handle with cached type and capability information.
// Passing it back to its interpreter refers to the original Python object.
type Object struct {
	obj value.Object
}

func (o Object) unwrap() value.Object { return o.obj }

func (o Object) handle() *value.Ref { return o.obj.Handle() }

func (o Object) ref() uint32 { return o.obj.Ref() }

// Type is the handle's Python class, or "object" when no name crossed with it.
func (o Object) Type() string { return o.obj.Type() }

// IsCallable reports whether the guest object can be called.
func (o Object) IsCallable() bool { return o.obj.IsCallable() }

// IsIterable reports whether the guest object is an iterator.
func (o Object) IsIterable() bool { return o.obj.IsIterable() }

// AsObject returns the opaque handle, or an error for copied data.
func (v Value) AsObject() (Object, error) {
	x, ok := v.val.(value.Object)
	if !ok {
		return Object{}, conversionError(v, "object")
	}
	return Object{obj: x}, nil
}

// PythonError describes a guest exception. Use errors.As with *PythonError
// to inspect its Type, Message, and Raw traceback.
type PythonError = value.Exception

// Exception builds an exception Value. Returning it from a HostFunc raises it;
// use [Raise] to return an exception through the error result instead.
func Exception(typ, msg string) Value {
	return wrapValue(value.NewException(typ, msg))
}

// Raise creates an error for a HostFunc to raise as a Python exception.
// Unknown exception classes and ordinary Go errors become HostError,
// a subclass of RuntimeError.
func Raise(typ, msg string) error {
	return value.NewException(typ, msg)
}

// walk recurses over an iterable or nested Value, applying a closure fn to each.
func walk(v Value, fn func(v Value) bool) bool {
	if !fn(v) {
		return false
	}

	if items, err := v.AsList(); err == nil {
		return walkAll(items, fn)
	}
	if items, err := v.AsTuple(); err == nil {
		return walkAll(items, fn)
	}
	if items, err := v.AsSet(); err == nil {
		return walkAll(items, fn)
	}
	if items, err := v.AsFrozenSet(); err == nil {
		return walkAll(items, fn)
	}
	if entries, err := v.AsDict(); err == nil {
		for _, entry := range entries {
			if !walk(entry.Key, fn) || !walk(entry.Val, fn) {
				return false
			}
		}
	}

	return true
}

func walkAll(items []Value, fn func(v Value) bool) bool {
	for _, item := range items {
		if !walk(item, fn) {
			return false
		}
	}
	return true
}
