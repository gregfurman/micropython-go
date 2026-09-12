package codec

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/value"
)

// encodeFailure carries an error out of the encoder's recursion. The arms
// below return records rather than (record, error) pairs so the shape of a
// value tree stays readable; EncodeInto turns this back into a plain error.
type encodeFailure struct{ err error }

// encoder writes one value tree into one arena. It is per-operation: the codec
// itself owns no transfer memory.
type encoder struct {
	codec *Codec
	arena *memory.Arena
	depth int
}

// EncodeInto writes v as a value tree rooted at ptr, taking every nested record
// and payload from a. The arena owns all of it: a caller releases the tree by
// resetting the arena, never by freeing anything the tree points at.
//
// ptr must already be reserved from a, so that nested payloads land after the
// root rather than on top of it.
func (c *Codec) EncodeInto(a *memory.Arena, ptr int32, v any) error {
	// Lower is the one place an arbitrary Go value becomes a Python one: it
	// owns the numeric conversions, the reflect fallback for slices and maps,
	// the JSON fallback for everything else, and the depth limit. Encoding
	// starts from the closed model it produces.
	lowered, err := value.Lower(v)
	if err != nil {
		return err
	}

	return c.encodeValueInto(a, ptr, lowered)
}

func (c *Codec) encodeValueInto(a *memory.Arena, ptr int32, v value.Value) (err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}

		failure, ok := recovered.(encodeFailure)
		if !ok {
			panic(recovered)
		}

		err = failure.err
	}()

	e := &encoder{codec: c, arena: a}
	e.put(ptr, e.encode(v))

	return nil
}

// EncodeErrorInto writes target as the exception the guest will raise.
func (c *Codec) EncodeErrorInto(a *memory.Arena, ptr int32, target error) error {
	// An unnamed type lets the guest apply its own default, HostError, which
	// keeps a failed host callback distinguishable from an interpreter error.
	typ, msg := "", target.Error()
	if pyErr, ok := errors.AsType[*value.Exception](target); ok {
		typ, msg = pyErr.Type(), pyErr.Message()
	}

	if strings.ContainsRune(typ, '\x04') {
		return fmt.Errorf("exception type %q contains the field separator", typ)
	}

	return c.encodeValueInto(a, ptr, value.NewException(typ, msg))
}

// EncodeEmptyErrorInto writes an exception with no payload, the fallback for
// when even the error text will not fit.
func (c *Codec) EncodeEmptyErrorInto(a *memory.Arena, ptr int32) error {
	e := &encoder{codec: c, arena: a}

	defer func() { _ = recover() }()

	e.put(ptr, Value{Kind: KindException})

	return nil
}

func (e *encoder) fail(err error) { panic(encodeFailure{err}) }

func (e *encoder) must(ptr int32, err error) int32 {
	if err != nil {
		e.fail(err)
	}

	return ptr
}

// encode turns one semantic value into its 12-byte record, allocating whatever
// the record points at from the arena first.
func (e *encoder) encode(v value.Value) Value {
	// Lower counts depth on the way in from `any`, but a caller can hand us a
	// value tree it built by hand, which never passed through that counter.
	if e.depth > value.MaxDepth {
		e.fail(fmt.Errorf("micropython: value nested deeper than %d levels", value.MaxDepth))
	}

	e.depth++
	defer func() { e.depth-- }()

	switch x := v.(type) {
	case nil, value.None:
		return Value{Kind: KindNone}

	case value.Bool:
		var word uint32
		if x {
			word = 1
		}

		return Value{Kind: KindBool, W1: word}

	case value.Int:
		bits := uint64(int64(x))
		return Value{Kind: KindInt, W1: uint32(bits), W2: uint32(bits >> 32)}

	case value.Float:
		bits := math.Float64bits(float64(x))
		return Value{Kind: KindFloat, W1: uint32(bits), W2: uint32(bits >> 32)}

	case value.BigInt:
		return e.blob(KindBigint, []byte(x.Unwrap().String()))

	case value.Str:
		return e.blob(KindStr, []byte(x))

	case value.Bytes:
		return e.blob(KindBytes, x)

	case *value.Exception:
		return e.blob(KindException, []byte(x.Type()+"\x04"+x.Message()))

	case value.ListValue:
		return e.seq(KindList, x)

	case value.TupleValue:
		return e.seq(KindTuple, x)

	case value.SetValue:
		return e.seq(KindSet, x)

	case value.FrozenSetValue:
		return e.seq(KindFrozenSet, x)

	case value.DictValue:
		return e.dict(x)

	case value.Object:
		if x.Handle() == nil {
			e.fail(fmt.Errorf("micropython: %s is not bound to an interpreter", x.Type()))
		}

		id, err := e.codec.refs.Lookup(x.Handle())
		if err != nil {
			e.fail(err)
		}

		return Value{Kind: KindObject, W1: id}
	}

	// Invalid values keep their reason behind the interface; Lift is the only
	// way to it from here.
	if err, ok := value.Lift(v).(error); ok {
		e.fail(err)
	}

	e.fail(fmt.Errorf("micropython: cannot pass %s to Python", v.Type()))

	return Value{}
}

func (e *encoder) blob(kind Kind, data []byte) Value {
	if len(data) == 0 {
		return Value{Kind: kind}
	}

	ptr := e.must(e.arena.Bytes(data))

	return Value{Kind: kind, W1: uint32(len(data)), W2: uint32(ptr)}
}

// seq lays out the elements of a list, tuple, set, or frozenset as one run of
// records. The run is reserved before the elements are encoded, so whatever
// they allocate lands after it.
func (e *encoder) seq(kind Kind, items []value.Value) Value {
	if len(items) == 0 {
		return Value{Kind: kind}
	}

	if len(items) > math.MaxInt32/ValueSize {
		e.fail(fmt.Errorf("sequence too large: %d entries", len(items)))
	}

	block := e.must(e.arena.New(int32(len(items)) * ValueSize))
	for i, item := range items {
		e.put(block+int32(i)*ValueSize, e.encode(item))
	}

	return Value{Kind: kind, W1: uint32(len(items)), W2: uint32(block)}
}

// dict lays out entries as alternating key and value records, which is the one
// container shape the ABI spells out rather than nesting.
func (e *encoder) dict(entries []value.Item) Value {
	if len(entries) == 0 {
		return Value{Kind: KindDict}
	}

	if len(entries) > math.MaxInt32/(2*ValueSize) {
		e.fail(fmt.Errorf("dict too large: %d entries", len(entries)))
	}

	block := e.must(e.arena.New(int32(len(entries)) * 2 * ValueSize))
	for i, entry := range entries {
		e.put(block+int32(2*i)*ValueSize, e.encode(entry.Key))
		e.put(block+int32(2*i+1)*ValueSize, e.encode(entry.Val))
	}

	return Value{Kind: KindDict, W1: uint32(len(entries)), W2: uint32(block)}
}

func (e *encoder) put(ptr int32, v Value) {
	buf, err := e.arena.View(ptr, ValueSize)
	if err != nil {
		e.fail(err)
	}

	v.MarshalWords(buf)
}
