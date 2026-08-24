package host

import (
	"bytes"
	"fmt"
)

const (
	wasmPageSize = 65536

	// The generated module's own maxMem, which it does not export. Growing
	// past it fails, which is the same answer the guest would get.
	maxMemoryPages = 65536
)

// Snapshot is an immutable interpreter frozen at a point in time.
type Snapshot struct {
	memory []byte

	// Where the copy belongs, which is also where the shadow stack ends. Kept
	// so a restore can refuse a module laid out differently rather than
	// scribble over it.
	base int32
}

// Snapshot copies the interpreter as it stands. The caller must hold the
// instance and no call may be in flight, which is checked rather than assumed:
// a shadow stack above its base means live C frames belonging to a host call
// that will not exist on restore.
func (a *ABI) Snapshot() (*Snapshot, error) {
	if err := a.status(); err != nil {
		return nil, err
	}
	if sp := *a.mod.X__stack_pointer(); sp != a.base {
		return nil, fmt.Errorf("micropython: cannot snapshot mid-call (stack pointer at %d, base %d)", sp, a.base)
	}

	// Everything below the base is shadow stack, which holds nothing between
	// calls. Skipping it takes a third off the copy.
	return &Snapshot{
		memory: bytes.Clone(a.mem()[a.base:]),
		base:   a.base,
	}, nil
}

// Restore builds a new interpreter from the snapshot.
//
// It skips _initialize, because the snapshot was taken after it ran: gc_init
// and mp_init have already happened and their results are in the memory being
// laid down.
func (s *Snapshot) Restore() (*ABI, error) {
	a := newABI()
	if err := s.restore(a); err != nil {
		return nil, err
	}
	return a, nil
}

// RestoreInto lays the snapshot back down over a running interpreter, which
// afterwards is the snapshot.
func (a *ABI) RestoreInto(s *Snapshot) error {
	if err := a.status(); err != nil {
		return err
	}
	if err := s.restore(a); err != nil {
		return err
	}

	a.dec.reset()
	a.enc.Reset()
	a.cancelled.Store(false)

	return nil
}

func (s *Snapshot) restore(a *ABI) error {
	if s.base != a.base {
		return fmt.Errorf("micropython: snapshot is from a module with a %d-byte stack, this one has %d", s.base, a.base)
	}

	mem := a.mod.Xmemory()
	want := int(s.base) + len(s.memory)

	if grow := want - len(*mem.Slice()); grow > 0 {
		// The guest can call sbrk (the malloc-backed output and scratch
		// buffers do) so a snapshot may be wider than the module it lands in.
		pages := int64((grow + wasmPageSize - 1) / wasmPageSize)
		if mem.Grow(pages, maxMemoryPages) < 0 {
			return fmt.Errorf("micropython: cannot grow memory to %d bytes to restore", want)
		}
	}

	copy((*mem.Slice())[s.base:], s.memory)
	*a.mod.X__stack_pointer() = a.base
	return nil
}
