package codec

import (
	"fmt"

	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/value"
)

// Refs owns the guest references a codec hands out and takes back. Track wraps
// an id the guest just minted; Lookup reports the id an owned ref names, or an
// error if it belongs to another interpreter or an older timeline.
type Refs interface {
	Track(id uint32) *value.Ref
	Lookup(*value.Ref) (uint32, error)
}

type Codec struct {
	mem  *memory.Memory
	refs Refs
}

func New(m *memory.Memory, refs Refs) *Codec {
	return &Codec{
		mem:  m,
		refs: refs,
	}
}

func (c *Codec) Encode(ptr int32, v any) error {
	return c.encodeAt(ptr, v)
}

// releaseGuest releases allocations made while serialising a Python value.
// Strings and bytes point into live MicroPython objects and are only borrowed.
func (c *Codec) releaseGuest(v Value) error {
	switch v.Kind {
	case KindBigint, KindException:
		c.mem.Free(int32(v.W2))
	case KindObject:
		c.mem.Free(int32(v.W2 &^ KindObjectAttrMask))
	case KindList, KindTuple, KindSet, KindFrozenSet:
		return c.releaseGuestBlock(v, ValueSize, 1)
	case KindDict:
		return c.releaseGuestBlock(v, 2*ValueSize, 2)
	}
	return nil
}

func (c *Codec) releaseGuestBlock(v Value, stride, perEntry int32) error {
	length, ptr, empty, err := header(v)
	if err != nil || empty {
		return err
	}
	if length > (1<<31-1)/stride {
		return fmt.Errorf("container too large: %d entries", length)
	}

	var firstErr error
	fail := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	for j := range length {
		for k := range perEntry {
			child, err := c.valueAt(ptr + j*stride + k*ValueSize)
			if err == nil {
				err = c.releaseGuest(child)
			}
			fail(err)
		}
	}
	c.mem.Free(ptr)
	return firstErr
}

func (c *Codec) Consume(ptr int32) (value.Value, error) {
	v, err := c.valueAt(ptr)
	if err != nil {
		return nil, err
	}

	out, decodeErr := c.decode(v)
	if relErr := c.releaseGuest(v); relErr != nil && decodeErr == nil {
		return out, relErr
	}

	return out, decodeErr
}
