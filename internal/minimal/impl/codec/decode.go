package codec

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
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

func (c *Codec) lift(v Value) (any, error) {
	switch v.Kind {
	case KindNull:
		return nil, errors.New("null value")
	case KindNone:
		return nil, nil
	case KindBool:
		return v.W1 != 0, nil
	case KindInt:
		return int32(v.W1), nil
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
		return c.mem.ReadString(int32(v.W2), int32(v.W1))
	case KindBytes:
		return c.mem.Read(int32(v.W2), int32(v.W1))
	case KindCallable, KindObject, KindRef:
		return nil, fmt.Errorf("kind %d: references not supported yet", int32(v.Kind))

	case KindList, KindTuple:
		length := int32(v.W1)
		ptr := int32(v.W2)

		if length == 0 || ptr == 0 {
			return []any{}, nil
		}

		defer c.mem.Free(ptr)

		res := make([]any, length)
		for j := range length {
			itemVal, err := c.valueAt(ptr + (j * ValueSize))
			if err != nil {
				return nil, err
			}
			itemGo, err := c.lift(itemVal)
			if err != nil {
				return nil, err
			}
			res[j] = itemGo
		}
		return res, nil
	case KindDict:
		numPairs := int32(v.W1)
		ptr := int32(v.W2)

		if numPairs == 0 || ptr == 0 {
			return map[any]any{}, nil
		}

		defer c.mem.Free(ptr)

		// TODO: ensure we only ever pass comparable?
		res := make(map[any]any, numPairs)
		for j := range numPairs {
			base := ptr + (j * 2 * ValueSize)

			keyVal, err := c.valueAt(base)
			if err != nil {
				return nil, err
			}
			key, err := c.lift(keyVal)
			if err != nil {
				return nil, err
			}

			valVal, err := c.valueAt(base + ValueSize) // valVal is murderous
			if err != nil {
				return nil, err
			}
			val, err := c.lift(valVal)
			if err != nil {
				return nil, err
			}

			res[key] = val
		}
		return res, nil

	case KindException:
		msg, err := c.mem.ReadString(int32(v.W2), int32(v.W1))
		if err != nil {
			return nil, err
		}
		if t, rest, ok := strings.Cut(msg, "\x04"); ok {
			return nil, &PythonError{Type: t, Msg: rest}
		}
		return nil, &PythonError{Msg: msg}
	}

	return nil, fmt.Errorf("unsupported kind: %d", int32(v.Kind))
}
