package codec

import "github.com/gregfurman/micropython-go/internal/minimal/impl/memory"

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

func (c *Codec) Borrow(ptr int32) (any, error) {
	v, err := c.valueAt(ptr)
	if err != nil {
		return nil, err
	}
	return c.lift(v)
}

func (c *Codec) Consume(ptr int32) (any, error) {
	v, err := c.valueAt(ptr)
	if err != nil {
		return nil, err
	}
	out, liftErr := c.lift(v)
	c.release(v)
	return out, liftErr
}

func (c *Codec) release(v Value) {
	// NOTE: primitives/scalars don't borrow, so no release necessary.
	switch v.Kind {
	case KindStr, KindBytes, KindBigint, KindException:
		c.mem.Free(int32(v.W2))

	case KindList, KindTuple:
		for j := int32(0); j < int32(v.W1); j++ {
			if child, err := c.valueAt(int32(v.W2) + j*ValueSize); err == nil {
				c.release(child)
			}
		}
		c.mem.Free(int32(v.W2))
	case KindDict:
		for j := int32(0); j < int32(v.W1); j++ {
			base := int32(v.W2) + j*2*ValueSize
			for _, off := range [...]int32{0, ValueSize} {
				if child, err := c.valueAt(base + off); err == nil {
					c.release(child)
				}
			}
		}
		c.mem.Free(int32(v.W2))
	}
}
