// Package value is the closed set of Python values the host can build, and the
// wire format they cross in. Each carries both directions: lower into the
// format build/wasm_build.c decodes, lift into the plain Go value a caller
// sees.
//
// Closed is the point: a value built from these can only be something Python
// has, so the compiler refuses a Go type with no equivalent rather than the
// encoder finding some answer for it.
package value

import (
	"errors"
	"fmt"
	"math"
)

// matching the PK_* enum in build/wasm_pack.h
const (
	TagNone = iota
	TagFalse
	TagTrue
	TagInt
	TagFloat
	TagStr
	TagBytes
	TagList
	TagTuple
	TagDict
	TagSet
	TagFrozenSet
	TagException
)

const MaxDepth = 32

type Value interface {
	lower(w Writer)
	lift() any
	Type() string
}

func Lower(v Value, w Writer) { v.lower(w) }

func Lift(v Value) any { return v.lift() }

type Writer interface {
	Tag(byte)
	U32(uint32)
	U64(uint64)
	Raw([]byte)
	RawString(string)
	Enter() bool
	Leave()
	Fail(error)
}

// Map builds the Go map a Python dict becomes, from alternating lifted keys
// and values. String keys give a map[string]any, anything else a map[any]any.
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

func blob(w Writer, n int) bool {
	if int64(n) > math.MaxUint32 {
		w.Fail(fmt.Errorf("micropython: %d bytes is too large to pass", n))
		return false
	}
	w.U32(uint32(n))
	return true
}

func lowerItems(w Writer, tag byte, values []Value) {
	if !w.Enter() {
		return
	}
	w.Tag(tag)
	w.U32(uint32(len(values)))
	for _, v := range values {
		v.lower(w)
	}
	w.Leave()
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

func (None) Type() string   { return "NoneType" }
func (None) lower(w Writer) { w.Tag(TagNone) }
func (None) lift() any      { return nil }

type Bool bool

func (Bool) Type() string { return "bool" }
func (b Bool) lower(w Writer) {
	if b {
		w.Tag(TagTrue)
		return
	}
	w.Tag(TagFalse)
}
func (b Bool) lift() any { return bool(b) }

type Int int64

func (Int) Type() string { return "int" }
func (i Int) lower(w Writer) {
	w.Tag(TagInt)
	w.U64(uint64(i))
}
func (i Int) lift() any { return int64(i) }

type Float float64

func (Float) Type() string { return "float" }
func (f Float) lower(w Writer) {
	w.Tag(TagFloat)
	w.U64(math.Float64bits(float64(f)))
}
func (f Float) lift() any { return float64(f) }

type Str string

func (Str) Type() string { return "str" }
func (s Str) lower(w Writer) {
	w.Tag(TagStr)
	if blob(w, len(s)) {
		w.RawString(string(s))
	}
}
func (s Str) lift() any { return string(s) }

type Bytes []byte

func (Bytes) Type() string { return "bytes" }
func (b Bytes) lower(w Writer) {
	w.Tag(TagBytes)
	if blob(w, len(b)) {
		w.Raw(b)
	}
}
func (b Bytes) lift() any { return []byte(b) }

// --- containers -------------------------------------------------------------
//
// Each kind appears once: the type the guest sends back, the constructor that
// builds one, and what it lowers to.

type (
	Tuple     []any
	Set       []any
	FrozenSet []any
)

func NewSet(items ...Value) Value {
	return setValue(items)
}

func NewList(items ...Value) Value {
	return listValue(items)
}

func NewTuple(items ...Value) Value {
	return tupleValue(items)
}

func NewFrozenSet(items ...Value) Value {
	return frozenSetValue(items)
}

func NewDict(entries ...Item) Value {
	out := make(dictValue, len(entries))
	copy(out, entries)
	return out
}

type listValue []Value

func (listValue) Type() string     { return "list" }
func (l listValue) lower(w Writer) { lowerItems(w, TagList, l) }
func (l listValue) lift() any      { return lift(l) }

type tupleValue []Value

func (tupleValue) Type() string     { return "tuple" }
func (t tupleValue) lower(w Writer) { lowerItems(w, TagTuple, t) }
func (t tupleValue) lift() any      { return Tuple(lift(t)) }

type setValue []Value

func (setValue) Type() string     { return "set" }
func (s setValue) lower(w Writer) { lowerItems(w, TagSet, s) }
func (s setValue) lift() any      { return Set(lift(s)) }

type frozenSetValue []Value

func (frozenSetValue) Type() string     { return "frozenset" }
func (f frozenSetValue) lower(w Writer) { lowerItems(w, TagFrozenSet, f) }
func (f frozenSetValue) lift() any      { return FrozenSet(lift(f)) }

type dictValue []Item

type Item struct {
	Key Value
	Val Value
}

func (dictValue) Type() string { return "dict" }
func (d dictValue) lower(w Writer) {
	if !w.Enter() {
		return
	}
	w.Tag(TagDict)
	w.U32(uint32(len(d)))
	for _, item := range d {
		item.Key.lower(w)
		item.Val.lower(w)
	}
	w.Leave()
}

func (d dictValue) lift() any {
	kv := make([]any, 0, 2*len(d))
	for _, entry := range d {
		kv = append(kv, entry.Key.lift(), entry.Val.lift())
	}
	return Map(kv)
}

// --- values with no Go equivalent --------------------------------------------

// Object is a Python value with no Go equivalent (e.g a class, an
// arbitrary instance) where only the type and repr survive the crossing.
//
// Object is declared but not currently supported.
type Object struct {
	Type string
	Repr string
}

func (o Object) String() string { return o.Repr }

var ErrNoCallable = errors.New("micropython: a Callable cannot be passed to the guest: host functions are not supported in this build")

// Callable is declared but not supported
type Callable struct {
	Name string
	Fn   func(args []Value, kwargs map[string]Value) (Value, error)
}

func (Callable) Type() string   { return "callable" }
func (Callable) lower(w Writer) { w.Fail(ErrNoCallable) }
func (c Callable) lift() any    { return c }

// ------------------------------------------------------------------

type invalid struct{ err error }

// Invalid is a value that cannot be lowered, carrying the reason.
func Invalid(err error) Value { return invalid{err} }

func (invalid) Type() string     { return "invalid" }
func (i invalid) lower(w Writer) { w.Fail(i.err) }
func (i invalid) lift() any      { return i.err }
