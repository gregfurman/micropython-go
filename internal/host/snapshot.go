package host

import (
	"fmt"
)

type Snapshot struct {
	memory  []byte
	stack   int32
	scratch int32

	// Guest memory holds host functions as ids, so the registry that resolves
	// them is part of the state a snapshot has to capture.
	registry map[int32]HostFunc
	counter  int32
}

func (s *Snapshot) Restore() (*Module, error) {
	i := newModule()
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
	i.disp.restore(s.registry, s.counter)
	return i, nil
}
