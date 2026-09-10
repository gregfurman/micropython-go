package codec

import (
	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/value"
)

// Refs owns the guest references a codec hands out and takes back.
type Refs interface {
	Retain(id uint32) (*value.Ref, error)
	Lookup(*value.Ref) (uint32, error)
	Release(id uint32) bool
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
