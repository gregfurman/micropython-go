package memory

import (
	"errors"
	"fmt"
	"math"
)

// ErrArenaFull reports a transfer region too small for the value tree written
// into it. It is not ErrGuestOOM: the guest heap is fine, the caller's buffer
// is not, so the operation can be retried against a larger one.
var ErrArenaFull = errors.New("transfer arena full")

// arenaAlign matches the alignment of mp_value_t on the guest side.
const arenaAlign = 4

// Arena is a bump allocator over one span of guest memory. Nothing in it is
// freed individually: a Mark's reset rewinds every allocation made after it,
// and Free releases the span whole.
//
// An arena either owns its span, having taken it from the guest heap, or views
// one somebody else supplied. An owning arena satisfies a request too large for
// the span from the heap instead and frees those spills on reset. A viewing
// arena cannot spill: a heap pointer would outlive the call that lent the span,
// and nothing on the other side would free it, so it reports ErrArenaFull.
type Arena struct {
	mem  *Memory
	ptrs []int32
	base int32
	size int32
	next int32
	owns bool
}

// NewArena takes size bytes from the guest heap for an arena to bump through.
// The caller keeps it for the life of the interpreter and Marks it per
// operation; Free returns the span.
func (m *Memory) NewArena(size int32) (*Arena, error) {
	if size < 0 {
		return nil, fmt.Errorf("%w: arena size %d", ErrInvalidMemory, size)
	}
	ptr := m.Alloc(size)
	if ptr == 0 && size != 0 {
		return nil, ErrGuestOOM
	}
	return &Arena{mem: m, base: ptr, size: size, owns: true}, nil
}

// ArenaAt bump-allocates inside a span the host does not own, which is how a
// guest hands over an output buffer: the guest picked the address and reclaims
// it when the call returns. It mirrors output_arena_init on the C side.
func (m *Memory) ArenaAt(ptr, size int32) *Arena {
	return &Arena{mem: m, base: ptr, size: size}
}

// Free releases the arena's spills and, if it owns one, its span. It is safe to
// call twice.
func (a *Arena) Free() {
	if a == nil || a.mem == nil {
		return
	}
	for _, ptr := range a.ptrs {
		a.mem.Free(ptr)
	}
	a.ptrs = nil
	if a.owns {
		a.mem.Free(a.base)
	}
	a.mem = nil
}

// Mark records the bump position so a caller can rewind to it, freeing
// whatever spilled past the span in between. The idiom is one line per
// operation:
//
//	defer arena.Mark()()
func (a *Arena) Mark() (reset func()) {
	ptrs, next := len(a.ptrs), a.next
	return func() {
		for _, ptr := range a.ptrs[ptrs:] {
			a.mem.Free(ptr)
		}
		a.ptrs = a.ptrs[:ptrs]
		a.next = next
	}
}

// New reserves size aligned bytes. The pointer stays valid until the enclosing
// Mark resets or the arena is freed.
func (a *Arena) New(size int32) (int32, error) {
	if size < 0 {
		return 0, fmt.Errorf("%w: allocation of %d bytes", ErrInvalidMemory, size)
	}

	// A negative next means the alignment overflowed, which only a span near
	// the top of a 2GiB memory reaches. Treat it as full rather than wrap.
	next := (a.next + arenaAlign - 1) &^ (arenaAlign - 1)
	if next >= 0 && next <= a.size && size <= a.size-next {
		a.next = next + size
		return a.base + next, nil
	}

	if !a.owns {
		return 0, fmt.Errorf("%w: %d bytes into a %d byte region", ErrArenaFull, size, a.size)
	}

	ptr := a.mem.Alloc(size)
	if ptr == 0 && size != 0 {
		return 0, ErrGuestOOM
	}
	a.ptrs = append(a.ptrs, ptr)
	return ptr, nil
}

// Bytes copies b into the arena. An empty slice takes no space and returns the
// null pointer, which is how the ABI spells a zero-length payload.
func (a *Arena) Bytes(b []byte) (int32, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if int64(len(b)) > math.MaxInt32 {
		return 0, fmt.Errorf("blob too large: %d bytes", len(b))
	}
	ptr, err := a.New(int32(len(b)))
	if err != nil {
		return 0, err
	}
	if err := a.mem.Write(ptr, b); err != nil {
		return 0, err
	}
	return ptr, nil
}

// String copies s into the arena. Payloads in this ABI carry their length, so
// there is no terminator; CString is for the guest entry points that strlen.
func (a *Arena) String(s string) (int32, error) {
	return a.Bytes([]byte(s))
}

// CString copies s into the arena with a NUL terminator, for guest entry
// points that take a bare char*.
func (a *Arena) CString(s string) (int32, error) {
	if int64(len(s))+1 > math.MaxInt32 {
		return 0, fmt.Errorf("string too large: %d bytes", len(s))
	}
	ptr, err := a.New(int32(len(s)) + 1)
	if err != nil {
		return 0, err
	}
	buf, err := a.View(ptr, int32(len(s))+1)
	if err != nil {
		return 0, err
	}
	copy(buf, s)
	buf[len(s)] = 0
	return ptr, nil
}

// View exposes arena bytes for writing in place, which is how fixed-size
// records are filled without a staging copy.
func (a *Arena) View(ptr, length int32) ([]byte, error) {
	return a.mem.View(ptr, length)
}

// ArenaState is an arena's position, saved alongside a memory image. The span
// itself lives in that image, so restoring both together resumes where the
// snapshot left off.
type ArenaState struct {
	base int32
	size int32
	next int32
	owns bool
}

func (a *Arena) Save() ArenaState {
	return ArenaState{base: a.base, size: a.size, next: a.next, owns: a.owns}
}

// Load rebinds an arena to a saved position. Spills are dropped rather than
// freed: they came from a guest heap the restored image has already rewound,
// so freeing them now would corrupt it.
func (a *Arena) Load(m *Memory, s ArenaState) {
	a.mem = m
	a.ptrs = nil
	a.base, a.size, a.next, a.owns = s.base, s.size, s.next, s.owns
}
