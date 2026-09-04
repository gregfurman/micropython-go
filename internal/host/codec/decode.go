package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/gregfurman/micropython-go/internal/value"
)

func (c *Codec) valueAt(ptr int32) (Value, error) {
	b, err := c.mem.View(ptr, ValueSize)
	if err != nil {
		return Value{}, err
	}

	var v Value
	v.UnmarshalWords(b)
	return v, nil
}

func (c *Codec) decode(v Value) (value.Value, error) {
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

	case KindObject:
		attributes := v.W2 & KindObjectAttrMask
		infoPtr := int32(v.W2 &^ KindObjectAttrMask)
		header, err := c.mem.Read(infoPtr, 4)
		if err != nil {
			return nil, fmt.Errorf("object info: %w", err)
		}
		length := binary.LittleEndian.Uint32(header)
		if length > math.MaxInt32 {
			return nil, fmt.Errorf("object info length too large: %d", length)
		}

		blob, err := c.mem.ReadString(infoPtr+4, int32(length))
		if err != nil {
			return nil, err
		}

		isIterable := (attributes & KindObjectIterable) != 0
		isCallable := (attributes & KindObjectCallable) != 0

		typ, repr, _ := strings.Cut(blob, "\x04")
		return value.NewObject(typ, repr, c.refs.Track(v.W1), isIterable, isCallable), nil

	case KindRef:
		return nil, fmt.Errorf(
			"kind %d: references not supported yet",
			int32(v.Kind),
		)

	default:
		return nil, fmt.Errorf(
			"unsupported kind: %d",
			int32(v.Kind),
		)
	}
}
