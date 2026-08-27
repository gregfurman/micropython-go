package codec

import (
	"encoding/binary"
	"math"
)

const ValueSize = 12

type Value struct {
	Kind Kind
	W1   uint32
	W2   uint32
}

func (v Value) Float() float64 {
	return math.Float64frombits(uint64(v.W1) | uint64(v.W2)<<32)
}

func (v Value) Int() int32 { return int32(v.W1) }

func (v *Value) UnmarshalWords(b []byte) {
	v.Kind = Kind(int32(binary.LittleEndian.Uint32(b[0:])))
	v.W1 = binary.LittleEndian.Uint32(b[4:])
	v.W2 = binary.LittleEndian.Uint32(b[8:])
}

func (v *Value) MarshalWords(b []byte) {
	binary.LittleEndian.PutUint32(b[0:], uint32(v.Kind))
	binary.LittleEndian.PutUint32(b[4:], v.W1)
	binary.LittleEndian.PutUint32(b[8:], v.W2)
}

type PythonError struct {
	Msg  string
	Type string
}

func (p *PythonError) Error() string {
	if p.Msg != "" {
		return p.Msg
	}
	return p.Type
}

func (p *PythonError) Is(target error) bool {
	t, ok := target.(*PythonError)
	return ok && t.Type == p.Type
}

type Encodable interface {
	EncodeTo(c *Codec, ptr int32) error
}

type None struct{}

func (None) EncodeTo(c *Codec, ptr int32) error {
	b, err := c.mem.View(ptr, ValueSize)
	if err != nil {
		return err
	}
	v := Value{Kind: KindNone}
	v.MarshalWords(b)
	return nil
}

type Bool bool

func (b Bool) EncodeTo(c *Codec, ptr int32) error {
	buf, err := c.mem.View(ptr, ValueSize)
	if err != nil {
		return err
	}
	v := Value{Kind: KindBool, W1: 0}
	if b {
		v.W1 = 1
	}
	v.MarshalWords(buf)
	return nil
}

type Int int32

func (i Int) EncodeTo(c *Codec, ptr int32) error {
	b, err := c.mem.View(ptr, ValueSize)
	if err != nil {
		return err
	}
	v := Value{Kind: KindInt, W1: uint32(i)}
	v.MarshalWords(b)
	return nil
}

type Float float64

func (f Float) EncodeTo(c *Codec, ptr int32) error {
	b, err := c.mem.View(ptr, ValueSize)
	if err != nil {
		return err
	}
	bits := math.Float64bits(float64(f))
	v := Value{Kind: KindFloat, W1: uint32(bits), W2: uint32(bits >> 32)}
	v.MarshalWords(b)
	return nil
}

type Str string

func (s Str) EncodeTo(c *Codec, ptr int32) error {
	strPtr, freeFn, err := c.mem.WriteString(string(s))
	if err != nil {
		return err
	}
	b, err := c.mem.View(ptr, ValueSize)
	if err != nil {
		freeFn()
		return err
	}
	v := Value{Kind: KindStr, W1: uint32(len(s)), W2: uint32(strPtr)}
	v.MarshalWords(b)
	return nil
}

type Bytes []byte

func (by Bytes) EncodeTo(c *Codec, ptr int32) error {
	bufPtr, freeFn, err := c.mem.WriteBytes(by)
	if err != nil {
		return err
	}
	b, err := c.mem.View(ptr, ValueSize)
	if err != nil {
		freeFn()
		return err
	}
	v := Value{Kind: KindBytes, W1: uint32(len(by)), W2: uint32(bufPtr)}
	v.MarshalWords(b)
	return nil
}

type List []Encodable

func (l List) EncodeTo(c *Codec, ptr int32) error {
	return encodeSequence(c, ptr, KindList, l)
}

type Tuple []Encodable

func (t Tuple) EncodeTo(c *Codec, ptr int32) error {
	return encodeSequence(c, ptr, KindTuple, t)
}

func encodeSequence(c *Codec, ptr int32, kind Kind, seq []Encodable) error {
	length := int32(len(seq))
	if length == 0 {
		b, err := c.mem.View(ptr, ValueSize)
		if err != nil {
			return err
		}
		v := Value{Kind: kind}
		v.MarshalWords(b)
		return nil
	}

	arrPtr, freeArr, err := c.mem.WriteBytes(make([]byte, length*ValueSize))
	if err != nil {
		return err
	}

	for i, item := range seq {
		itemPtr := arrPtr + (int32(i) * ValueSize)
		if err := item.EncodeTo(c, itemPtr); err != nil {
			freeArr()
			return err
		}
	}

	b, err := c.mem.View(ptr, ValueSize)
	if err != nil {
		freeArr()
		return err
	}
	v := Value{Kind: kind, W1: uint32(length), W2: uint32(arrPtr)}
	v.MarshalWords(b)
	return nil
}

type KV struct{ Key, Val Encodable }
type Dict []KV

func (d Dict) EncodeTo(c *Codec, ptr int32) error {
	length := int32(len(d))
	if length == 0 {
		b, err := c.mem.View(ptr, ValueSize)
		if err != nil {
			return err
		}
		v := Value{Kind: KindDict}
		v.MarshalWords(b)
		return nil
	}

	arrPtr, freeArr, err := c.mem.WriteBytes(make([]byte, length*2*ValueSize))
	if err != nil {
		return err
	}

	for i, kv := range d {
		base := arrPtr + (int32(i) * 2 * ValueSize)
		if err := kv.Key.EncodeTo(c, base); err != nil {
			freeArr()
			return err
		}
		if err := kv.Val.EncodeTo(c, base+ValueSize); err != nil {
			freeArr()
			return err
		}
	}

	b, err := c.mem.View(ptr, ValueSize)
	if err != nil {
		freeArr()
		return err
	}
	v := Value{Kind: KindDict, W1: uint32(length), W2: uint32(arrPtr)}
	v.MarshalWords(b)
	return nil
}

// type Ref struct {
// 	inst *Instance
// 	id   uint32
// 	kind Kind // KindCallable or KindObject
// }

// func (r *Ref) Close() error {
// 	if r.inst == nil {
// 		return nil
// 	}
// 	// FIXME: Use refs_free to release the host reference, not the memory allocator
// 	r.inst.mod.Xfree(int32(r.id))
// 	r.inst = nil
// 	return nil
// }
//
// func (r *Ref) Callable() bool { return r.kind == KindCallable }
