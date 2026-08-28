package memory

import (
	"bytes"
	"errors"
	"fmt"
	"math"

	wasi "github.com/gregfurman/micropython-go/internal/micropython"
)

type Memory struct {
	backing func() []byte
	alloc   func(n int32) int32
	free    func(ptr int32)
}

func New(mod interface {
	Xmemory() wasi.Memory
	Xfree(v0 int32)
	Xmalloc(v0 int32) int32
}) *Memory {
	return &Memory{
		backing: func() []byte { return *mod.Xmemory().Slice() },
		alloc:   mod.Xmalloc,
		free:    mod.Xfree,
	}
}

var (
	ErrInvalidMemory = errors.New("invalid memory")
	ErrGuestOOM      = errors.New("guest malloc failed")
)

func (m *Memory) view(ptr, length int32) ([]byte, bool) {
	if ptr < 0 || length < 0 {
		return nil, false
	}
	mem := m.backing()
	end := int64(ptr) + int64(length)
	if end > int64(len(mem)) {
		return nil, false
	}
	return mem[ptr:end:end], true
}

func (m *Memory) View(ptr, length int32) ([]byte, error) {
	b, ok := m.view(ptr, length)
	if !ok {
		return nil, fmt.Errorf("%w: [%d,+%d) of %d", ErrInvalidMemory, ptr, length, len(m.backing()))
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

// Encodes two natural (i.e non-negative) numbers as a single natural number.
// Requires that k1 > k2 (i.e monontonic). See Cantor pairing https://en.wikipedia.org/wiki/Pairing_function.
func Encode(k1, k2 uint64) uint64 {
	// NOTE: if it turns out this is spitting out numbers that're too big, consider using
	// http://szudzik.com/ElegantPairing.pdf
	return ((k1+k2)*(k1+k2+1) + k2) / 2
}

// Decode gives us the original k1 and k2 values from the encoded value z.
// Note, that this is the inverse of the original Cantor pairing algorithm.
func Decode(z uint64) (k1, k2 uint64) {
	w := uint64(math.Floor((math.Sqrt(float64(8*z+1)) - 1) / 2))
	t := ((w * w) + w) / 2
	k2 = z - t
	k1 = w - k2
	return
}
