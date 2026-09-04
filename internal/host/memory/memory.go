package memory

import (
	"bytes"
	"errors"
	"fmt"
	"math"
)

const (
	// PageSize is the granularity wasm grows a linear memory in.
	PageSize = 64 * 1024

	// MaxPages is as far as a wasm32 memory can grow, 4GiB.
	MaxPages = 65536
)

var (
	ErrInvalidMemory = errors.New("invalid memory")
	ErrGuestOOM      = errors.New("guest malloc failed")
)

// Allocator is the guest's malloc and free, which live in the module that
// imports this memory.
type Allocator interface {
	Xmalloc(v0 int32) int32
	Xfree(v0 int32)
}

// Memory is the linear memory the guest imports. The host owns the backing
// slice, so it is built before the module that grows it.
type Memory struct {
	buf []byte
	max int64

	alloc func(n int32) int32
	free  func(ptr int32)
}

// New returns a memory of pages, growable to maxPages. pages cannot be less
// than the module was linked against, see -Wl,--initial-memory in
// build/build.sh.
func New(pages, maxPages int64) *Memory {
	return &Memory{
		buf: make([]byte, pages*PageSize),
		max: maxPages,
	}
}

// Bind attaches the guest allocator. Alloc cannot be called before this.
func (m *Memory) Bind(a Allocator) {
	m.alloc, m.free = a.Xmalloc, a.Xfree
}

// Slice hands the module the backing store. The module keeps the pointer, not
// the slice, so Grow can swap what is underneath it.
func (m *Memory) Slice() *[]byte {
	return &m.buf
}

// Grow adds delta pages, capped by max and by this memory's own ceiling, and
// reports the page count from before. It returns -1 if it cannot, which is what
// memory.grow expects.
func (m *Memory) Grow(delta, max int64) int64 {
	size := int64(len(m.buf))
	old := size / PageSize
	if delta == 0 {
		return old
	}

	want := old + delta
	max = min(max, m.max, int64(math.MaxInt)/PageSize)
	if want > max || want < old {
		return -1
	}

	m.buf = append(m.buf, make([]byte, want*PageSize-size)...)
	return old
}

// Image copies the memory out whole, for a snapshot to hold.
func (m *Memory) Image() []byte {
	return bytes.Clone(m.buf)
}

// Load writes an image back over the memory, growing first if it no longer
// fits. Anything past the image is left as it was.
func (m *Memory) Load(image []byte) error {
	if grow := int64(len(image) - len(m.buf)); grow > 0 {
		pages := (grow + PageSize - 1) / PageSize
		if m.Grow(pages, MaxPages) < 0 {
			return fmt.Errorf("%w: cannot grow to %d bytes", ErrInvalidMemory, len(image))
		}
	}
	copy(m.buf, image)
	return nil
}

func (m *Memory) view(ptr, length int32) ([]byte, bool) {
	if ptr < 0 || length < 0 {
		return nil, false
	}
	end := int64(ptr) + int64(length)
	if end > int64(len(m.buf)) {
		return nil, false
	}
	return m.buf[ptr:end:end], true
}

func (m *Memory) View(ptr, length int32) ([]byte, error) {
	b, ok := m.view(ptr, length)
	if !ok {
		return nil, fmt.Errorf("%w: [%d,+%d) of %d", ErrInvalidMemory, ptr, length, len(m.buf))
	}
	return b, nil
}

func (m *Memory) Alloc(n int32) int32 {
	if n <= 0 {
		return 0
	}
	p := m.alloc(n)
	if p == 0 {
		return 0
	}
	if _, ok := m.view(p, n); !ok {
		m.free(p)
		return 0
	}
	return p
}

func (m *Memory) Free(ptr int32) {
	if ptr != 0 {
		m.free(ptr)
	}
}

func (m *Memory) Read(ptr, n int32) ([]byte, error) {
	b, err := m.View(ptr, n)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(b), nil
}

func (m *Memory) ReadString(ptr, n int32) (string, error) {
	b, err := m.View(ptr, n)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (m *Memory) WriteCString(s string) (int32, func(), error) {
	size := int64(len(s)) + 1
	if size > math.MaxInt32 {
		return 0, nil, fmt.Errorf("string too large: %d bytes", len(s))
	}

	ptr := m.Alloc(int32(size))
	if ptr == 0 {
		return 0, nil, ErrGuestOOM
	}

	buf, err := m.View(ptr, int32(size))
	if err != nil {
		m.Free(ptr)
		return 0, nil, fmt.Errorf("malloc returned out-of-range pointer %d: %w", ptr, err)
	}

	copy(buf, s)
	buf[len(s)] = 0

	return ptr, func() { m.Free(ptr) }, nil
}

func (m *Memory) WriteBytes(b []byte) (int32, func(), error) {
	if int64(len(b)) > math.MaxInt32 {
		return 0, nil, fmt.Errorf("blob too large: %d bytes", len(b))
	}
	ptr := m.Alloc(int32(len(b)))
	if ptr == 0 {
		return 0, nil, ErrGuestOOM
	}
	buf, err := m.View(ptr, int32(len(b)))
	if err != nil {
		m.Free(ptr)
		return 0, nil, fmt.Errorf("malloc returned out-of-range pointer %d: %w", ptr, err)
	}
	copy(buf, b)
	return ptr, func() { m.Free(ptr) }, nil
}

func (m *Memory) WriteString(s string) (int32, func(), error) {
	return m.WriteBytes([]byte(s))
}
