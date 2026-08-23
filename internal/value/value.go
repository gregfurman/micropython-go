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

// MaxDepth matches PK_MAX_DEPTH. The guest refuses anything deeper, so there is
// no point sending it.
const MaxDepth = 32

var ()

// Value is a Python value the host has built. The set is closed: only the types
// in this package implement it.
// The method set is unexported apart from Type, which does two jobs: it seals
// the interface, so only this package implements it, and it keeps the surface
// clean on a type the public package aliases -- Exception is both a value and
// an error, and a caller inspecting one should not see the wire format.
type Value interface {
	lower(w Writer)
	lift() any
	Type() string
}

// Lower writes v through w. A method would be neater, but an exported one
// shows up on every aliased type.
func Lower(v Value, w Writer) { v.lower(w) }

// Lift returns the plain Go value v stands for.
func Lift(v Value) any { return v.lift() }

type (
	// None is Python's None.
	None struct{}

	// Bool is a Python bool.
	Bool bool

	// Int is a Python int. One outside int64 cannot be built here, though the
	// guest can still send one back.
	Int int64

	// Float is a Python float.
	Float float64

	// Str is a Python str.
	Str string

	// Bytes is a Python bytes.
	Bytes []byte

	// Callable is a Go function the guest could call. Declared but not
	// supported: lowering one fails, since the guest side does not exist.
	Callable struct {
		Name string
		Fn   func(args []Value, kwargs map[string]Value) (Value, error)
	}

	list      []Value
	tuple     []Value
	set       []Value
	frozenSet []Value
	dict      []Item
)

// Item is one entry of a Dict.
type Item struct {
	Key Value
	Val Value
}

// What the decoder produces, and so what Lift returns.

// Tuple is a Python tuple. It is distinct from []any so that the round trip
// back into Python can preserve tuple-ness, which JSON could not.
type Tuple []any

// Set is a Python set. Go has no set type, so it arrives as a slice -- but a
// distinct one, so a reader is not misled into depending on the order. A set
// has none: these are the elements in whatever order the guest's hash table
// held them.
type Set []any

// FrozenSet is a Python frozenset, separate from Set only so a reader can tell
// which one the guest had.
type FrozenSet []any

// NewList builds a Python list.
func NewList(items ...Value) Value { return list(items) }

// NewTuple builds a Python tuple.
func NewTuple(items ...Value) Value { return tuple(items) }

// NewSet builds a Python set. Duplicates are the guest's to collapse and an
// unhashable member is the guest's to refuse, exactly as in Python.
func NewSet(items ...Value) Value { return set(items) }

// NewFrozenSet builds a Python frozenset.
func NewFrozenSet(items ...Value) Value { return frozenSet(items) }

// NewDict builds a Python dict, keeping the order it was given.
func NewDict(entries ...Item) Value {
	out := make(dict, len(entries))
	copy(out, entries)
	return out
}

func (None) Type() string      { return "NoneType" }
func (Bool) Type() string      { return "bool" }
func (Int) Type() string       { return "int" }
func (Float) Type() string     { return "float" }
func (Str) Type() string       { return "str" }
func (Bytes) Type() string     { return "bytes" }
func (Callable) Type() string  { return "callable" }
func (list) Type() string      { return "list" }
func (tuple) Type() string     { return "tuple" }
func (set) Type() string       { return "set" }
func (frozenSet) Type() string { return "frozenset" }
func (dict) Type() string      { return "dict" }

// ------------------------------------------------------------------

func (None) lower(w Writer) { w.Tag(TagNone) }

func (b Bool) lower(w Writer) {
	if b {
		w.Tag(TagTrue)
		return
	}
	w.Tag(TagFalse)
}

func (i Int) lower(w Writer) {
	w.Tag(TagInt)
	w.U64(uint64(i))
}

func (f Float) lower(w Writer) {
	w.Tag(TagFloat)
	w.U64(math.Float64bits(float64(f)))
}

func (s Str) lower(w Writer) {
	w.Tag(TagStr)
	if blob(w, len(s)) {
		w.RawString(string(s))
	}
}

func (b Bytes) lower(w Writer) {
	w.Tag(TagBytes)
	if blob(w, len(b)) {
		w.Raw(b)
	}
}

// ErrNoCallable is what lowering a Callable reports
var ErrNoCallable = errors.New("micropython: a Callable cannot be passed to the guest: host functions are not supported in this build")

func (Callable) lower(w Writer) { w.Fail(ErrNoCallable) }

func (l list) lower(w Writer)      { lowerItems(w, TagList, l) }
func (t tuple) lower(w Writer)     { lowerItems(w, TagTuple, t) }
func (s set) lower(w Writer)       { lowerItems(w, TagSet, s) }
func (f frozenSet) lower(w Writer) { lowerItems(w, TagFrozenSet, f) }

func (d dict) lower(w Writer) {
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

// ------------------------------------------------------------------

func (None) lift() any    { return nil }
func (b Bool) lift() any  { return bool(b) }
func (i Int) lift() any   { return int64(i) }
func (f Float) lift() any { return float64(f) }
func (s Str) lift() any   { return string(s) }
func (b Bytes) lift() any { return []byte(b) }

// Lift returns the Go function itself, which is the only thing it stands for:
// nothing decodes into a Callable, since the guest cannot send one.
func (c Callable) lift() any  { return c }
func (l list) lift() any      { return lift(l) }
func (t tuple) lift() any     { return Tuple(lift(t)) }
func (s set) lift() any       { return Set(lift(s)) }
func (f frozenSet) lift() any { return FrozenSet(lift(f)) }

func (d dict) lift() any {
	kv := make([]any, 0, 2*len(d))
	for _, entry := range d {
		kv = append(kv, entry.Key.lift(), entry.Val.lift())
	}
	return Map(kv)
}

func lift[S ~[]Value](s S) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v.lift()
	}
	return out
}

// ------------------------------------------------------------------

// Writer is what Lower writes through. The format is described here and
// implemented by whoever is collecting the bytes; internal/host has the one
// that fills the module's scratch buffer.
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

// blob writes a length and the bytes it counts.
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

// Map builds the Go map a Python dict becomes, from alternating lifted keys
// and values. String keys give a map[string]any, anything else a map[any]any.
//
// Shared with the decoder, which must answer this the same way. The flat shape
// is its: values arrive one callback at a time.
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

// MapKey makes a value usable as a Go map key. Python hashes tuples,
// frozensets and bytes; Go panics on them, which a guest could trigger with
// {(1, 2): "x"}, so those stand in as their rendering.
func MapKey(v any) any {
	switch v.(type) {
	case []any, Tuple, Set, FrozenSet, []byte, map[string]any, map[any]any:
		return fmt.Sprintf("%T%v", v, v)
	}
	return v
}

// Invalid is a value that cannot be lowered, carrying the reason.
func Invalid(err error) Value { return invalid{err} }

type invalid struct{ err error }

func (i invalid) lower(w Writer) { w.Fail(i.err) }
func (i invalid) lift() any      { return i.err }
func (invalid) Type() string     { return "invalid" }
