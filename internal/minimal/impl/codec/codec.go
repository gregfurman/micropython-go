package codec

import (
	"fmt"

	"github.com/gregfurman/micropython-go/internal/minimal/impl/memory"
)

type Codec struct {
	mem *memory.Memory
}

func New(m *memory.Memory) *Codec {
	return &Codec{
		mem: m,
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
	case KindList, KindTuple:
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

func (c *Codec) Borrow(ptr int32) (any, error) {
	v, err := c.valueAt(ptr)
	if err != nil {
		return nil, err
	}
	return c.decode(v)
}

func (c *Codec) Consume(ptr int32) (any, error) {
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
