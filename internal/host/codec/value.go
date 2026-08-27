package codec

import (
	"encoding/binary"
	"math"

	"github.com/gregfurman/micropython-go/internal/value"
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

func (v Value) Int() int64 { return int64(uint64(v.W1) | uint64(v.W2)<<32) }

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

type PythonError = value.Exception

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
