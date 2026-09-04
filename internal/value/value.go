package value

import (
	"errors"
	"fmt"
	"math/big"
)

const MaxDepth = 32

type Value interface {
	lift() any
	Type() string
}

func Lift(v Value) any { return v.lift() }

// Map builds the Go map a Python dict becomes, from alternating lifted keys
// and values. String keys give a `map[string]any`, anything else a `map[any]any`.
func Map(kv []any) any {
	strings := true
	for i := 0; i+1 < len(kv); i += 2 {
		if _, ok := kv[i].(string); !ok {
			strings = false
			break
		}
	}

	if strings {
		out := make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1]
		}
		return out
	}

	out := make(map[any]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out[MapKey(kv[i])] = kv[i+1]
	}
	return out
}

func MapKey(v any) any {
	switch v.(type) {
	case []any, Tuple, Set, FrozenSet, []byte, map[string]any, map[any]any:
		// HACK: just return as string representation if used as a map key...
		return fmt.Sprintf("%T%v", v, v)
	}
	return v
}

func lift[S ~[]Value](s S) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v.lift()
	}
	return out
}

// --- scalars ----------------------------------------------------------------

type None struct{}

func (None) Type() string { return "NoneType" }
func (None) lift() any    { return nil }

type Bool bool

func (Bool) Type() string { return "bool" }
func (b Bool) lift() any  { return bool(b) }

type Int int64

func (Int) Type() string { return "int" }
func (i Int) lift() any  { return int64(i) }

type Float float64

func (Float) Type() string { return "float" }
func (f Float) lift() any  { return float64(f) }

type Str string

func (Str) Type() string { return "str" }
func (s Str) lift() any  { return string(s) }

type Bytes []byte

func (Bytes) Type() string { return "bytes" }
func (b Bytes) lift() any  { return []byte(b) }

type BigInt struct {
	n *big.Int
}

func NewBigInt(n *big.Int) Value {
	if n == nil {
		return BigInt{n: new(big.Int)}
	}
	return BigInt{n: new(big.Int).Set(n)}
}

func (b *BigInt) Unwrap() *big.Int {
	return b.n
}

func (BigInt) Type() string { return "int" }

func (n BigInt) lift() any {
	if n.n == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(n.n)
}

// ---------------------------------------------------------------------

type (
	Tuple     []any
	Set       []any
	FrozenSet []any
)

func NewSet(items ...Value) Value {
	return SetValue(items)
}

func NewList(items ...Value) Value {
	return ListValue(items)
}

func NewTuple(items ...Value) Value {
	return TupleValue(items)
}

func NewFrozenSet(items ...Value) Value {
	return FrozenSetValue(items)
}

func NewDict(entries ...Item) Value {
	out := make(DictValue, len(entries))
	copy(out, entries)
	return out
}

type ListValue []Value

func (ListValue) Type() string { return "list" }
func (l ListValue) lift() any  { return lift(l) }

type TupleValue []Value

func (TupleValue) Type() string { return "tuple" }
func (t TupleValue) lift() any  { return lift(t) }

type SetValue []Value

func (SetValue) Type() string { return "set" }
func (s SetValue) lift() any  { return lift(s) }

type FrozenSetValue []Value

func (FrozenSetValue) Type() string { return "frozenset" }
func (f FrozenSetValue) lift() any  { return lift(f) }

type DictValue []Item

type Item struct {
	Key Value
	Val Value
}

func (DictValue) Type() string { return "dict" }
func (d DictValue) lift() any {
	kv := make([]any, 0, 2*len(d))
	for _, entry := range d {
		kv = append(kv, entry.Key.lift(), entry.Val.lift())
	}
	return Map(kv)
}

// ---------------------------------------------------------------------

// Ref owns one guest reference. Object is copied freely, so the ref it names
// is released when the last copy sharing this pointer becomes unreachable.
// Owner and epoch identify the interpreter and the timeline that minted it.
type Ref struct {
	id    uint32
	owner any
	epoch uint64
}

func NewRef(id uint32, owner any, epoch uint64) *Ref {
	return &Ref{id: id, owner: owner, epoch: epoch}
}

func (r *Ref) ID() uint32 {
	if r == nil {
		return 0
	}
	return r.id
}

func (r *Ref) Owner() any {
	if r == nil {
		return nil
	}
	return r.owner
}

func (r *Ref) Epoch() uint64 {
	if r == nil {
		return 0
	}
	return r.epoch
}

type Object struct {
	typeName string
	repr     string
	ref      *Ref

	isCallable bool
	isIterable bool
}

func NewObject(typ, repr string, ref *Ref, iterable, callable bool) Object {
	return Object{
		typeName:   typ,
		repr:       repr,
		ref:        ref,
		isCallable: callable,
		isIterable: iterable,
	}
}

func (o Object) Type() string {
	return o.typeName
}

func (o Object) Repr() string {
	return o.repr
}

func (o Object) String() string {
	return o.repr
}

func (o Object) Ref() uint32 {
	return o.ref.ID()
}

func (o Object) Handle() *Ref {
	return o.ref
}

func (o Object) IsCallable() bool {
	return o.isCallable
}

func (o Object) IsIterable() bool {
	return o.isIterable
}

func (o Object) lift() any {
	return o
}

var ErrNoCallable = errors.New("micropython: this Callable is not bound to an interpreter")

var ErrNoIterator = errors.New("micropython: this Iterator is not bound to an interpreter")

// ------------------------------------------------------------------

type invalid struct{ err error }

// Invalid is a value that cannot be lowered, carrying the reason.
func Invalid(err error) Value { return invalid{err} }

func (invalid) Type() string { return "invalid" }
func (i invalid) lift() any  { return i.err }
