package host

import (
	"fmt"
	"io"
)

type Snapshot struct {
	memory  []byte
	stack   int32
	scratch int32

	// A snapshot is not a pure memory image: guest memory refers to host
	// functions by id, so the registry that resolves them has to travel with it,
	// and the same reasoning applies to the sink those functions print to.
	registry map[int32]HostFunc
	counter  int32
	stdout   io.Writer
}

// Stdout reports the sink interpreters restored from this snapshot will use.
func (s *Snapshot) Stdout() io.Writer { return s.stdout }

func (s *Snapshot) Restore() (*Instance, error) {
	i := newModule(s.stdout)
	mem := i.mod.Xmemory()
	if grow := len(s.memory) - len(*mem.Slice()); grow > 0 {
		pages := int64((grow + wasmPageSize - 1) / wasmPageSize)
		if mem.Grow(pages, maxMemoryPages) < 0 {
			return nil, fmt.Errorf("cannot grow memory to %d bytes", len(s.memory))
		}
	}
	copy(*mem.Slice(), s.memory)
	*i.mod.X__stack_pointer() = s.stack
	i.scratch = s.scratch
	i.restore(s.registry, s.counter)
	return i, nil
}
