package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strings"

	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/value"
)

// maxDecodeDepth bounds the recursion through a copied value tree. Guest
// containers are copied rather than walked now, so this guards against a
// pathological or corrupted tree rather than against reference cycles.
const maxDecodeDepth = 128

// decoder reads one result. Arena checks memory bounds; remaining limits the
// number of records visited if corrupted pointers alias or cycle.
type decoder struct {
	codec     *Codec
	arena     *memory.Arena
	refs      []uint32
	remaining int32
}

// Decode copies a guest result into Go values and releases all IDs acquired
// for that result, even when decoding fails. Each returned handle retains its
// own acquisition. The caller can reset its arena as soon as Decode returns.
func (c *Codec) Decode(ptr, size int32) (value.Value, error) {
	ids, err := c.readRefs(ptr, size)
	if err != nil {
		return nil, err
	}
	defer c.releaseRefs(ids)

	d := decoder{
		codec: c, arena: c.mem.ArenaAt(ptr, size),
		refs: ids, remaining: size / ValueSize,
	}

	return d.decodeAt(ptr, 0)
}

// ReleaseRefs discards a result without decoding it. Rejected callbacks use
// this to release their arguments without creating any Go handles.
func (c *Codec) ReleaseRefs(ptr, size int32) error {
	ids, err := c.readRefs(ptr, size)
	if err != nil {
		return err
	}

	c.releaseRefs(ids)

	return nil
}

func (c *Codec) releaseRefs(ids []uint32) {
	for _, id := range ids {
		c.refs.Release(id)
	}
}

func (c *Codec) readRefs(ptr, size int32) ([]uint32, error) {
	if ptr < 0 || ptr%4 != 0 || size < TransferSize {
		return nil, fmt.Errorf("invalid result region: [%d,+%d)", ptr, size)
	}

	b, err := c.mem.View(ptr, size)
	if err != nil {
		return nil, err
	}

	refsPtr := binary.LittleEndian.Uint32(b[ValueSize:])

	count := binary.LittleEndian.Uint32(b[ValueSize+4:])
	if count == 0 && refsPtr == 0 {
		return nil, nil
	}

	start := int64(refsPtr) - int64(ptr)
	if count == 0 || refsPtr%4 != 0 || start < TransferSize || start+int64(count)*4 > int64(size) {
		return nil, errors.New("invalid reference ledger")
	}

	ids := make([]uint32, count)
	for i := range ids {
		ids[i] = binary.LittleEndian.Uint32(b[start+int64(i)*4:])
		if ids[i] == 0 {
			return nil, errors.New("zero reference in ledger")
		}
	}
	// Object membership checks use binary search, not another ownership map.
	slices.Sort(ids)

	return ids, nil
}

func (d *decoder) decodeAt(ptr int32, depth int) (value.Value, error) {
	if ptr%4 != 0 {
		return nil, fmt.Errorf("unaligned value pointer %d", ptr)
	}

	b, err := d.arena.View(ptr, ValueSize)
	if err != nil {
		return nil, err
	}

	var v Value
	v.UnmarshalWords(b)

	return d.decode(v, depth)
}

func (d *decoder) decode(v Value, depth int) (value.Value, error) {
	if depth > maxDecodeDepth {
		return nil, fmt.Errorf("value nested deeper than %d levels", maxDecodeDepth)
	}

	if d.remaining == 0 {
		return nil, errors.New("value tree exceeds result record limit")
	}

	d.remaining--

	switch v.Kind {
	case KindNull:
		return nil, errors.New("null value")

	case KindNone:
		return value.None{}, nil

	case KindBool:
		return value.Bool(v.W1 != 0), nil

	case KindInt:
		return value.Int(v.Int()), nil

	case KindFloat:
		return value.Float(v.Float()), nil

	case KindBigint:
		b, err := d.arena.View(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}

		s := string(b)

		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, fmt.Errorf("bad bigint %q", s)
		}

		return value.NewBigInt(n), nil

	case KindStr:
		b, err := d.arena.View(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}

		return value.Str(string(b)), nil

	case KindBytes:
		b, err := d.arena.View(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}

		out := make(value.Bytes, len(b))
		copy(out, b)

		return out, nil

	case KindException:
		b, err := d.arena.View(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}

		typ, rest, _ := strings.Cut(string(b), "\x04")
		message, raw, _ := strings.Cut(rest, "\x04")

		return nil, value.FromGuest(
			raw,
			value.NewException(typ, message),
			false,
		)

	case KindList, KindTuple, KindSet, KindFrozenSet:
		items, err := d.decodeBlock(v, int64(v.W1), depth)
		if err != nil {
			return nil, err
		}

		switch v.Kind {
		case KindTuple:
			return value.TupleValue(items), nil
		case KindSet:
			return value.SetValue(items), nil
		case KindFrozenSet:
			return value.FrozenSetValue(items), nil
		default:
			return value.ListValue(items), nil
		}

	case KindDict:
		// Entries alternate key, value, so a dict of n pairs is a run of 2n
		// records rather than a second ABI structure.
		flat, err := d.decodeBlock(v, 2*int64(v.W1), depth)
		if err != nil {
			return nil, err
		}

		entries := make(value.DictValue, 0, len(flat)/2)
		for i := 0; i+1 < len(flat); i += 2 {
			entries = append(entries, value.Item{Key: flat[i], Val: flat[i+1]})
		}

		return entries, nil

	case KindObject:
		if v.W1 == 0 {
			return nil, errors.New("object has no reference")
		}

		if _, ok := slices.BinarySearch(d.refs, v.W1); !ok {
			return nil, fmt.Errorf("reference %d is absent from result ledger", v.W1)
		}

		ref, err := d.codec.refs.Retain(v.W1)
		if err != nil {
			return nil, err
		}

		class, err := d.class(v.W2)
		if err != nil {
			return nil, err
		}

		return value.NewObject(
			class,
			ref,
			v.W2&KindObjectIterable != 0,
			v.W2&KindObjectCallable != 0,
		), nil

	case KindRef:
		return nil, fmt.Errorf(
			"kind %d: references not supported yet",
			int32(v.Kind),
		)

	default:
		return nil, fmt.Errorf("unsupported kind: %d", int32(v.Kind))
	}
}

func (d *decoder) class(w2 uint32) (string, error) {
	ptr := int32(w2 &^ KindObjectAttrMask)
	if ptr == 0 {
		return "", nil
	}

	header, err := d.arena.View(ptr, 4)
	if err != nil {
		return "", err
	}

	length := int32(binary.LittleEndian.Uint32(header))
	if length <= 0 {
		return "", nil
	}

	name, err := d.arena.View(ptr+4, length)
	if err != nil {
		return "", err
	}

	return string(name), nil
}

func (d *decoder) decodeBlock(v Value, count int64, depth int) ([]value.Value, error) {
	if count == 0 {
		return nil, nil
	}

	if count < 0 || count > math.MaxInt32/ValueSize {
		return nil, fmt.Errorf("container of %d entries out of range", v.W1)
	}

	if v.W2 == 0 {
		return nil, fmt.Errorf("container of %d entries has no payload", v.W1)
	}

	block := int32(v.W2)

	if count > int64(d.remaining) {
		return nil, errors.New("container exceeds result record limit")
	}

	if _, err := d.arena.View(block, int32(count)*ValueSize); err != nil {
		return nil, err
	}

	items := make([]value.Value, count)
	for i := range items {
		item, err := d.decodeAt(block+int32(i)*ValueSize, depth+1)
		if err != nil {
			return nil, err
		}

		items[i] = item
	}

	return items, nil
}
