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

// Snapshot is an interpreter frozen at a point in time: its heap, its globals,
// every module it had imported and every function it had compiled.
//
// Restoring one is how several interpreters share the cost of starting.
// Everything MicroPython owns lives in linear memory -- the GC heap is a static
// array in it, and an mp_obj_t is an offset into it rather than a host pointer
// -- so a copy of that memory laid back down at the same addresses is an exact
// replica, with no fixups to apply. What a Snapshot does not carry is host-side
// state: the decoder, the trap record and the cancellation flag all start
// fresh, which is what makes a restored interpreter independent of the one it
// came from.
//
// A Snapshot is immutable and safe to restore from several goroutines at once.
// It holds its own copy of the memory, so it costs about what a live instance
// does, less the shadow stack.
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
// afterwards is the snapshot: every global, definition and import it had, and
// nothing that has happened since.
//
// This is the cheap path, and the reason Snapshot is worth having at all.
// Building an interpreter is mostly the 3MB of linear memory and the indirect
// function table, neither of which depends on what the interpreter has been
// doing -- so reusing them leaves only the copy.
func (a *ABI) RestoreInto(s *Snapshot) error {
	if err := a.status(); err != nil {
		return err
	}
	if err := s.restore(a); err != nil {
		return err
	}

	a.dec.reset()
	a.enc.reset()
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
		// The guest can call sbrk -- the malloc-backed output and scratch
		// buffers do -- so a snapshot may be wider than the module it lands in.
		pages := int64((grow + wasmPageSize - 1) / wasmPageSize)
		if mem.Grow(pages, maxMemoryPages) < 0 {
			return fmt.Errorf("micropython: cannot grow memory to %d bytes to restore", want)
		}
	}

	copy((*mem.Slice())[s.base:], s.memory)
	*a.mod.X__stack_pointer() = a.base
	return nil
}
