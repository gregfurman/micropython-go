package host

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"sync/atomic"

	"github.com/gregfurman/micropython-go/internal/host/codec"
	"github.com/gregfurman/micropython-go/internal/host/memory"
	wasi "github.com/gregfurman/micropython-go/internal/micropython"
)

const (
	maxHostArgs     = 8 // TODO: investigate + potentially autogen with cgo def gen
	wasmPageSize    = 64 * 1024
	maxMemoryPages  = 65536
	defaultHeapSize = 2 * wasmPageSize // give 128KB to start
)

type Module struct {
	mod *wasi.Module
	mem *memory.Memory

	codec *codec.Codec

	registry map[int32]HostFunc
	counter  int32

	cancelled atomic.Bool

	stdout io.Writer

	scratch int32
}

func NewModule(size uint, stdout io.Writer) (*Module, error) {
	if size == 0 {
		size = defaultHeapSize
	}

	i := newModule(stdout)
	if i.mod.Xinit_vm(int32(size), int32(maxHostArgs)) != 0 {
		return nil, memory.ErrGuestOOM
	}
	if i.scratch = i.mem.Alloc(codec.ValueSize); i.scratch == 0 {
		return nil, memory.ErrGuestOOM
	}
	return i, nil
}

func newModule(stdout io.Writer) *Module {
	if stdout == nil {
		stdout = io.Discard
	}
	i := &Module{
		registry: make(map[int32]HostFunc),
		stdout:   stdout,
	}

	i.mod = wasi.New(i)
	i.mem = memory.New(i.mod)
	i.codec = codec.New(i.mem)

	return i
}

// Begin starts an operation, clearing a cancel left over from the last one.
func (i *Module) Begin() {
	i.cancelled.Store(false)
}

func (i *Module) Cancel() {
	i.cancelled.Store(true)
}

// Decode converts a raw mp_obj_t word into a native Go value.
func (i *Module) Decode(objPtr int32) (any, error) {
	i.mod.Xobj_to_value(objPtr, i.scratch)
	return i.codec.Consume(i.scratch)
}

func (i *Module) Eval(code string) (any, error) {
	ptr, free, err := i.mem.WriteString(code)
	if err != nil {
		return nil, err
	}
	defer free()

	i.mod.Xeval(ptr, int32(len(code)), i.scratch)
	return i.codec.Consume(i.scratch)
}

func (i *Module) Exec(code string) error {
	ptr, free, err := i.mem.WriteString(code)
	if err != nil {
		return err
	}

	defer free()

	i.mod.Xexec(ptr, int32(len(code)), i.scratch)
	_, err = i.codec.Consume(i.scratch)
	return err
}

func (i *Module) Call(name string, args []any) (any, error) {
	namePtr, freeName, err := i.mem.WriteString(name)
	if err != nil {
		return nil, err
	}
	defer freeName()

	var argsPtr int32
	if len(args) > 0 {
		if len(args) > int(^uint32(0)>>1)/int(codec.ValueSize) {
			return nil, fmt.Errorf("too many arguments: %d", len(args))
		}
		argsPtr = i.mem.Alloc(int32(len(args)) * codec.ValueSize)
		if argsPtr == 0 {
			return nil, memory.ErrGuestOOM
		}
		encoded := int32(0)
		defer func() {
			if encoded >= 0 {
				i.codec.ReleaseHostBlock(argsPtr, encoded)
			}
			i.mem.Free(argsPtr)
		}()
		for n, arg := range args {
			if err := i.codec.Encode(argsPtr+int32(n)*codec.ValueSize, arg); err != nil {
				return nil, err
			}
			encoded++
		}
		// obj_from_value consumes every child allocation on the normal path.
		defer func() { encoded = -1 }()
	}

	i.mod.Xcall(namePtr, int32(len(name)), argsPtr, int32(len(args)), i.scratch)
	return i.codec.Consume(i.scratch)
}

func (i *Module) Set(name string, value any) error {
	namePtr, freeName, err := i.mem.WriteString(name)
	if err != nil {
		return err
	}
	defer freeName()
	valuePtr := i.mem.Alloc(codec.ValueSize)
	if valuePtr == 0 {
		return memory.ErrGuestOOM
	}
	defer i.mem.Free(valuePtr)
	if err := i.codec.Encode(valuePtr, value); err != nil {
		return err
	}
	i.mod.Xset_global(namePtr, int32(len(name)), valuePtr, i.scratch)
	_, err = i.codec.Consume(i.scratch)
	return err
}

func (i *Module) Snapshot() *Snapshot {
	return &Snapshot{
		memory:   bytes.Clone(*i.mod.Xmemory().Slice()),
		stack:    *i.mod.X__stack_pointer(),
		scratch:  i.scratch,
		registry: maps.Clone(i.registry),
		counter:  i.counter,
		stdout:   i.stdout,
	}
}

func (i *Module) Restore(s *Snapshot) error {
	mem := i.mod.Xmemory()
	if grow := len(s.memory) - len(*mem.Slice()); grow > 0 {
		pages := int64((grow + wasmPageSize - 1) / wasmPageSize)
		if mem.Grow(pages, maxMemoryPages) < 0 {
			return fmt.Errorf("cannot grow memory to %d bytes", len(s.memory))
		}
	}
	copy(*mem.Slice(), s.memory)
	*i.mod.X__stack_pointer() = s.stack
	i.scratch = s.scratch
	i.restore(s.registry, s.counter)
	i.cancelled.Store(false)
	return nil
}

// ---------------------------------------------------------------------

type HostFunc func(args []any) (any, error)

func (i *Module) DefineFunction(name string, fn HostFunc) error {
	if fn == nil {
		return errors.New("nil host func")
	}

	// qstr_from_str strlens this, so it must be NUL-terminated.
	ptr, free, err := i.mem.WriteCString(name)
	if err != nil {
		return err
	}
	defer free()

	id := i.register(fn)
	i.mod.Xdefine_function(ptr, id)
	return nil
}
