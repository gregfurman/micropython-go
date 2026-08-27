package codec

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
)

func (c *Codec) encodeAt(ptr int32, value any) error {
	if value == nil {
		return c.putValue(ptr, Value{Kind: KindNone})
	}

	switch v := value.(type) {
	case bool:
		var word uint32
		if v {
			word = 1
		}
		return c.putValue(ptr, Value{Kind: KindBool, W1: word})
	case int:
		return c.encodeInt64(ptr, int64(v))
	case int8:
		return c.encodeInt64(ptr, int64(v))
	case int16:
		return c.encodeInt64(ptr, int64(v))
	case int32:
		return c.encodeInt64(ptr, int64(v))
	case int64:
		return c.encodeInt64(ptr, v)
	case uint:
		return c.encodeUint64(ptr, uint64(v))
	case uint8:
		return c.encodeUint64(ptr, uint64(v))
	case uint16:
		return c.encodeUint64(ptr, uint64(v))
	case uint32:
		return c.encodeUint64(ptr, uint64(v))
	case uint64:
		return c.encodeUint64(ptr, v)
	case float32:
		return c.encodeFloat(ptr, float64(v))
	case float64:
		return c.encodeFloat(ptr, v)
	case string:
		return c.putBlob(ptr, KindStr, []byte(v))
	case []byte:
		return c.putBlob(ptr, KindBytes, v)
	case *big.Int:
		if v == nil {
			return c.putValue(ptr, Value{Kind: KindNone})
		}
		return c.putBlob(ptr, KindBigint, []byte(v.String()))
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Interface, reflect.Pointer:
		if rv.IsNil() {
			return c.putValue(ptr, Value{Kind: KindNone})
		}
		return c.encodeAt(ptr, rv.Elem().Interface())
	case reflect.Slice, reflect.Array:
		return c.encodeSequence(ptr, rv)
	case reflect.Map:
		return c.encodeMap(ptr, rv)
	}
	return fmt.Errorf("cannot encode %T", value)
}

func (c *Codec) encodeInt64(ptr int32, v int64) error {
	if v >= math.MinInt32 && v <= math.MaxInt32 {
		return c.putValue(ptr, Value{Kind: KindInt, W1: uint32(int32(v))})
	}
	return c.putBlob(ptr, KindBigint, []byte(strconv.FormatInt(v, 10)))
}

func (c *Codec) encodeUint64(ptr int32, v uint64) error {
	if v <= math.MaxInt32 {
		return c.putValue(ptr, Value{Kind: KindInt, W1: uint32(v)})
	}
	return c.putBlob(ptr, KindBigint, []byte(strconv.FormatUint(v, 10)))
}

func (c *Codec) encodeFloat(ptr int32, v float64) error {
	bits := math.Float64bits(v)
	return c.putValue(ptr, Value{Kind: KindFloat, W1: uint32(bits), W2: uint32(bits >> 32)})
}

func (c *Codec) putValue(ptr int32, v Value) error {
	b, err := c.mem.View(ptr, ValueSize)
	if err != nil {
		return err
	}
	v.MarshalWords(b)
	return nil
}

func (c *Codec) putBlob(ptr int32, kind Kind, data []byte) error {
	if len(data) == 0 {
		return c.putValue(ptr, Value{Kind: kind})
	}
	p, free, err := c.mem.WriteBytes(data)
	if err != nil {
		return err
	}
	if err := c.putValue(ptr, Value{Kind: kind, W1: uint32(len(data)), W2: uint32(p)}); err != nil {
		free()
		return err
	}
	return nil
}

func (c *Codec) encodeSequence(ptr int32, seq reflect.Value) error {
	n := seq.Len()
	if n == 0 {
		return c.putValue(ptr, Value{Kind: KindList})
	}
	if n > math.MaxInt32/ValueSize {
		return fmt.Errorf("sequence too large: %d entries", n)
	}
	block, free, err := c.mem.WriteBytes(make([]byte, n*ValueSize))
	if err != nil {
		return err
	}
	encoded := 0
	defer func() {
		if encoded < 0 {
			return
		}
		for i := 0; i < encoded; i++ {
			c.releaseHostAt(block + int32(i)*ValueSize)
		}
		free()
	}()
	for i := range n {
		if err := c.encodeAt(block+int32(i)*ValueSize, seq.Index(i).Interface()); err != nil {
			return err
		}
		encoded++
	}
	if err := c.putValue(ptr, Value{Kind: KindList, W1: uint32(n), W2: uint32(block)}); err != nil {
		return err
	}
	encoded = -1
	return nil
}

func (c *Codec) encodeMap(ptr int32, m reflect.Value) error {
	n := m.Len()
	if n == 0 {
		return c.putValue(ptr, Value{Kind: KindDict})
	}
	if n > math.MaxInt32/(2*ValueSize) {
		return fmt.Errorf("map too large: %d entries", n)
	}
	block, free, err := c.mem.WriteBytes(make([]byte, n*2*ValueSize))
	if err != nil {
		return err
	}
	encoded := 0
	defer func() {
		if encoded < 0 {
			return
		}
		for i := 0; i < encoded; i++ {
			c.releaseHostAt(block + int32(i)*ValueSize)
		}
		free()
	}()
	iter := m.MapRange()
	for iter.Next() {
		if err := c.encodeAt(block+int32(encoded)*ValueSize, iter.Key().Interface()); err != nil {
			return err
		}
		encoded++
		if err := c.encodeAt(block+int32(encoded)*ValueSize, iter.Value().Interface()); err != nil {
			return err
		}
		encoded++
	}
	if err := c.putValue(ptr, Value{Kind: KindDict, W1: uint32(n), W2: uint32(block)}); err != nil {
		return err
	}
	encoded = -1
	return nil
}

// releaseHostAt is only used to roll back a partial host-to-guest encoding.
// All pointer payloads in that direction were allocated by the host codec.
func (c *Codec) releaseHostAt(ptr int32) {
	v, err := c.valueAt(ptr)
	if err != nil {
		return
	}
	switch v.Kind {
	case KindStr, KindBytes, KindBigint, KindException:
		c.mem.Free(int32(v.W2))
	case KindList, KindTuple:
		for i := int32(0); i < int32(v.W1); i++ {
			c.releaseHostAt(int32(v.W2) + i*ValueSize)
		}
		c.mem.Free(int32(v.W2))
	case KindDict:
		for i := int32(0); i < int32(v.W1)*2; i++ {
			c.releaseHostAt(int32(v.W2) + i*ValueSize)
		}
		c.mem.Free(int32(v.W2))
	}
}

func (c *Codec) EncodeEmptyError(ptr int32) error {
	return c.putValue(ptr, Value{Kind: KindException})
}

func (c *Codec) EncodeError(ptr int32, target error) error {
	typ, msg := "RuntimeError", target.Error()
	if pyErr, ok := errors.AsType[*PythonError](target); ok {
		if pyErr.Type != "" {
			typ = pyErr.Type
		}
		msg = pyErr.Msg
	}
	if strings.ContainsRune(typ, '\x04') {
		return fmt.Errorf("exception type %q contains the field separator", typ)
	}
	return c.putBlob(ptr, KindException, []byte(typ+"\x04"+msg))
}
