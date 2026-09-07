package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/gregfurman/micropython-go/internal/host/abi"
	"github.com/gregfurman/micropython-go/internal/value"
)

// maxDecodeDepth bounds the recursion through a copied value tree. Guest
// containers are copied rather than walked now, so this guards against a
// pathological or corrupted tree rather than against reference cycles.
const maxDecodeDepth = 128

func (c *Codec) valueAt(ptr int32) (Value, error) {
	b, err := c.mem.View(ptr, ValueSize)
	if err != nil {
		return Value{}, err
	}

	var v Value
	v.UnmarshalWords(b)
	return v, nil
}

func (c *Codec) decode(v Value, depth int) (value.Value, error) {
	if depth > maxDecodeDepth {
		return nil, fmt.Errorf("value nested deeper than %d levels", maxDecodeDepth)
	}

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
		s, err := c.mem.ReadString(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}

		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, fmt.Errorf("bad bigint %q", s)
		}

		return value.NewBigInt(n), nil

	case KindStr:
		s, err := c.mem.ReadString(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}
		return value.Str(s), nil

	case KindBytes:
		b, err := c.mem.Read(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}
		return value.Bytes(b), nil

	case KindException:
		msg, err := c.mem.ReadString(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}

		typ, rest, _ := strings.Cut(msg, "\x04")
		message, raw, _ := strings.Cut(rest, "\x04")

		return nil, value.FromGuest(
			raw,
			value.NewException(typ, message),
			false,
		)

	case KindList, KindTuple, KindSet, KindFrozenSet:
		items, err := c.decodeBlock(v, int64(v.W1), depth)
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
		flat, err := c.decodeBlock(v, 2*int64(v.W1), depth)
		if err != nil {
			return nil, err
		}
		entries := make(value.DictValue, 0, len(flat)/2)
		for i := 0; i+1 < len(flat); i += 2 {
			entries = append(entries, value.Item{Key: flat[i], Val: flat[i+1]})
		}
		return entries, nil

	case KindObject:
		attributes := v.W2 & KindObjectAttrMask
		infoPtr := int32(v.W2 &^ KindObjectAttrMask)
		header, err := c.mem.View(infoPtr, abi.ObjectInfoSize)
		if err != nil {
			return nil, fmt.Errorf("object info: %w", err)
		}
		length := binary.LittleEndian.Uint32(header)
		if length > math.MaxInt32 {
			return nil, fmt.Errorf("object info length too large: %d", length)
		}
		if v.W1 == 0 || binary.LittleEndian.Uint32(header[4:]) != v.W1 {
			return nil, fmt.Errorf("object reference %d is not owned by this transfer", v.W1)
		}

		blob, err := c.mem.ReadString(infoPtr+abi.ObjectInfoSize, int32(length))
		if err != nil {
			return nil, err
		}

		isIterable := (attributes & KindObjectIterable) != 0
		isCallable := (attributes & KindObjectCallable) != 0

		typ, repr, _ := strings.Cut(blob, "\x04")
		ref := c.refs.Track(v.W1)
		// The Go handle now owns this acquisition. The transfer's final cleanup
		// will release only entries that decoding never reached or rejected.
		binary.LittleEndian.PutUint32(header[4:], 0)
		return value.NewObject(typ, repr, ref, isIterable, isCallable), nil

	case KindRef:
		return nil, fmt.Errorf(
			"kind %d: references not supported yet",
			int32(v.Kind),
		)

	default:
		return nil, fmt.Errorf("unsupported kind: %d", int32(v.Kind))
	}
}

// decodeBlock reads the run of count records a container points at. The run
// lives in the same transfer arena as its parent, so it is read directly
// rather than asked for an element at a time.
func (c *Codec) decodeBlock(v Value, count int64, depth int) ([]value.Value, error) {
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
	items := make([]value.Value, count)
	for i := range items {
		record, err := c.valueAt(block + int32(i)*ValueSize)
		if err != nil {
			return nil, err
		}
		item, err := c.decode(record, depth+1)
		if err != nil {
			return nil, err
		}
		items[i] = item
	}
	return items, nil
}
