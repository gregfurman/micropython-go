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

// Codec understands the ABI and nothing about who owns transfer memory. Every
// operation that needs space is handed an arena by its caller.
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

// Consume decodes the value tree rooted at ptr into Go-owned values. Every
// payload it reads belongs to the caller's transfer arena and stays valid only
// for the duration of this call, so nothing it returns aliases guest memory:
// once Consume returns, resetting that arena is safe.
//
// Object metadata is the one payload the guest still allocates outside the
// arena; decode releases each one as it reads it, so this frees nothing itself.
func (c *Codec) Consume(ptr int32) (value.Value, error) {
	v, err := c.valueAt(ptr)
	if err != nil {
		return nil, err
	}
	return c.decode(v, 0)
}
