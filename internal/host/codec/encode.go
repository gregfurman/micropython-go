package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"

	"github.com/gregfurman/micropython-go/internal/value"
	val "github.com/gregfurman/micropython-go/internal/value"
)

func (c *Codec) encodeAt(ptr int32, value any) error {
	if value == nil {
		return c.putValue(ptr, Value{Kind: KindNone})
	}

	switch v := value.(type) {
	case *val.Exception:
		return c.putBlob(ptr, KindException, []byte(v.Type()+"\x04"+v.Message()))
	case val.ListValue:
		return c.encodeSequenceKind(ptr, reflect.ValueOf([]val.Value(v)), KindList)
	case val.TupleValue:
		return c.encodeSequenceKind(ptr, reflect.ValueOf([]val.Value(v)), KindTuple)
	case val.SetValue:
		return c.encodeSequenceKind(ptr, reflect.ValueOf([]val.Value(v)), KindSet)
	case val.FrozenSetValue:
		return c.encodeSequenceKind(ptr, reflect.ValueOf([]val.Value(v)), KindFrozenSet)
	case val.Tuple:
		return c.encodeSequenceKind(ptr, reflect.ValueOf([]any(v)), KindTuple)
	case val.Set:
		return c.encodeSequenceKind(ptr, reflect.ValueOf([]any(v)), KindSet)
	case val.FrozenSet:
		return c.encodeSequenceKind(ptr, reflect.ValueOf([]any(v)), KindFrozenSet)
	case val.Object:
		if v.Handle() == nil {
			return fmt.Errorf("micropython: %s is not bound to an interpreter", v.Type())
		}
		id, err := c.refs.Lookup(v.Handle())
		if err != nil {
			return err
		}
		return c.putValue(ptr, Value{Kind: KindObject, W1: id})
	case val.Value:
		lifted := val.Lift(v)
		if err, ok := lifted.(error); ok {
			return err
		}
		return c.encodeAt(ptr, lifted)
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
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return c.encodeInt64(ptr, n)
		}
		f, err := v.Float64()
		if err != nil {
			return fmt.Errorf("cannot encode number %q: %w", v, err)
		}
		return c.encodeFloat(ptr, f)
	case *big.Int:
		return c.putBlob(ptr, KindBigint, []byte(v.String()))
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Interface, reflect.Pointer:
		if rv.IsNil() {
			return c.putValue(ptr, Value{Kind: KindNone})
		}
		// TODO: how should we handle interfaces being passed in?
		return c.encodeAt(ptr, rv.Elem().Interface())
	case reflect.Slice, reflect.Array:
		return c.encodeSequence(ptr, rv)
	case reflect.Map:
		return c.encodeMap(ptr, rv)
	}
	return c.encodeJSON(ptr, value)
}

func (c *Codec) encodeJSON(ptr int32, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("micropython: cannot pass %T to Python: %w", value, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var standard any
	if err := dec.Decode(&standard); err != nil {
		return fmt.Errorf("micropython: cannot parse %T for Python: %w", value, err)
	}
	return c.encodeAt(ptr, standard)
}

func (c *Codec) encodeInt64(ptr int32, v int64) error {
	bits := uint64(v)
	return c.putValue(ptr, Value{Kind: KindInt, W1: uint32(bits), W2: uint32(bits >> 32)})
}

func (c *Codec) encodeUint64(ptr int32, v uint64) error {
	if v > math.MaxInt64 {
		return fmt.Errorf("micropython: %d is too large to pass as an int", v)
	}
	return c.encodeInt64(ptr, int64(v))
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
	return c.encodeSequenceKind(ptr, seq, KindList)
}

func (c *Codec) encodeSequenceKind(ptr int32, seq reflect.Value, kind Kind) error {
	n := seq.Len()
	if n == 0 {
		return c.putValue(ptr, Value{Kind: kind})
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
	if err := c.putValue(ptr, Value{Kind: kind, W1: uint32(n), W2: uint32(block)}); err != nil {
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
	case KindList, KindTuple, KindSet, KindFrozenSet:
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

// ReleaseHostBlock rolls back values encoded by Go that were never handed to
// the guest. Once they are, releasing them is the guest's.
func (c *Codec) ReleaseHostBlock(ptr, count int32) {
	for i := int32(0); i < count; i++ {
		c.releaseHostAt(ptr + i*ValueSize)
	}
}

func (c *Codec) EncodeEmptyError(ptr int32) error {
	return c.putValue(ptr, Value{Kind: KindException})
}

func (c *Codec) EncodeError(ptr int32, target error) error {
	// An unnamed type lets the guest apply its own default, HostError, which
	// keeps a failed host callback distinguishable from an interpreter error.
	typ, msg := "", target.Error()
	if pyErr, ok := errors.AsType[*value.Exception](target); ok {
		typ, msg = pyErr.Type(), pyErr.Message()
	}
	if strings.ContainsRune(typ, '\x04') {
		return fmt.Errorf("exception type %q contains the field separator", typ)
	}
	return c.putBlob(ptr, KindException, []byte(typ+"\x04"+msg))
}
