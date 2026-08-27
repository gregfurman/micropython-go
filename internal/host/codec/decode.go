package codec

import (
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

func (c *Codec) decode(v Value) (any, error) {
	switch v.Kind {
	case KindNull:
		return nil, errors.New("null value")

	case KindNone:
		return nil, nil

	case KindBool:
		return v.W1 != 0, nil

	case KindInt:
		return v.Int(), nil

	case KindFloat:
		return v.Float(), nil

	case KindBigint:
		s, err := c.mem.ReadString(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, fmt.Errorf("bad bigint %q", s)
		}
		return n, nil

	case KindStr:
		s, err := c.mem.ReadString(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}
		return s, nil

	case KindBytes:
		b, err := c.mem.Read(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}
		return b, nil

	case KindList:
		items, err := c.decodeSeq(v)
		if err != nil {
			return nil, err
		}
		return items, nil

	case KindTuple:
		items, err := c.decodeSeq(v)
		if err != nil {
			return nil, err
		}
		return value.Tuple(items), nil

	case KindSet, KindFrozenSet:
		items, err := c.decodeSeq(v)
		if err != nil {
			return nil, err
		}
		if v.Kind == KindFrozenSet {
			return value.FrozenSet(items), nil
		}
		return value.Set(items), nil

	case KindDict:
		return c.decodeDict(v)

	case KindException:
		msg, err := c.mem.ReadString(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}
		typ, rest, _ := strings.Cut(msg, "\x04")
		message, raw, _ := strings.Cut(rest, "\x04")
		return nil, value.FromGuest(raw, value.NewException(typ, message), false)

	case KindObject:
		blob, err := c.mem.ReadString(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}
		typ, repr, _ := strings.Cut(blob, "\x04")
		return value.Object{Type: typ, Repr: repr}, nil

	case KindCallable, KindRef:
		return nil, fmt.Errorf("kind %d: references not supported yet", int32(v.Kind))
	}

	return nil, fmt.Errorf("unsupported kind: %d", int32(v.Kind))
}

func header(v Value) (length, ptr int32, empty bool, err error) {
	length, ptr = int32(v.W1), int32(v.W2)

	switch {
	case length == 0:
		return 0, 0, true, nil
	case length < 0:
		return 0, 0, false, fmt.Errorf("bad container length %d", uint32(v.W1))
	case ptr == 0:
		return 0, 0, false, fmt.Errorf("length %d with null pointer", length)
	}
	return length, ptr, false, nil
}

func (c *Codec) decodeSeq(v Value) ([]any, error) {
	length, ptr, empty, err := header(v)
	if err != nil || empty {
		return []any{}, err
	}
	if length > math.MaxInt32/ValueSize {
		return nil, fmt.Errorf("sequence too large: %d entries", length)
	}

	if _, err := c.mem.View(ptr, length*ValueSize); err != nil {
		return nil, fmt.Errorf("sequence block: %w", err)
	}

	out := make([]any, length)
	for j := range length {
		item, err := c.decodeAt(ptr + j*ValueSize)
		if err != nil {
			return nil, err
		}
		out[j] = item
	}
	return out, nil
}

func (c *Codec) decodeDict(v Value) (any, error) {
	numPairs, ptr, empty, err := header(v)
	if err != nil {
		return nil, err
	}
	if empty {
		return map[string]any{}, nil
	}
	if numPairs > math.MaxInt32/(2*ValueSize) {
		return nil, fmt.Errorf("dict too large: %d entries", numPairs)
	}

	if _, err := c.mem.View(ptr, numPairs*2*ValueSize); err != nil {
		return nil, fmt.Errorf("dict block: %w", err)
	}

	kv := make([]any, 0, numPairs*2)
	for j := range numPairs {
		base := ptr + j*2*ValueSize

		k, err := c.decodeAt(base)
		if err != nil {
			return nil, fmt.Errorf("dict key %d: %w", j, err)
		}
		val, err := c.decodeAt(base + ValueSize)
		if err != nil {
			return nil, fmt.Errorf("dict value %d: %w", j, err)
		}
		kv = append(kv, k, val)
	}
	return value.Map(kv), nil
}

func (c *Codec) decodeAt(ptr int32) (any, error) {
	v, err := c.valueAt(ptr)
	if err != nil {
		return nil, err
	}
	return c.decode(v)
}
