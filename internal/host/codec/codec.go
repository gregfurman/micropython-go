package codec

import (
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

// releaseGuest frees what the guest allocated to write a value. Strings and
// bytes are borrowed from live objects, and a container is a handle.
func (c *Codec) releaseGuest(v Value) {
	switch v.Kind {
	case KindBigint, KindException:
		c.mem.Free(int32(v.W2))
	case KindObject:
		c.mem.Free(int32(v.W2 &^ KindObjectAttrMask))
	}
}

// Consume decodes the value at ptr and releases what the guest allocated to
// write it. A container comes back as a Container for the caller to walk, since
// reaching into the guest is not the codec's to do.
func (c *Codec) Consume(ptr int32) (value.Value, Container, error) {
	v, err := c.valueAt(ptr)
	if err != nil {
		return nil, Container{}, err
	}
	defer c.releaseGuest(v)

	if box, ok, err := container(v); ok {
		return nil, box, err
	}

	out, err := c.decode(v)
	return out, Container{}, err
}
