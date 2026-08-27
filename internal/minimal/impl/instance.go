package impl

import (
	"errors"
	"os"

	wasi "github.com/gregfurman/micropython-go/internal/minimal"
	"github.com/gregfurman/micropython-go/internal/minimal/impl/codec"
	"github.com/gregfurman/micropython-go/internal/minimal/impl/memory"
)

const (
	maxHostArgs     = 8             // TODO: investigate + potentially autogen with cgo def gen
	defaultHeapSize = 2 * 64 * 1024 // NOTE: 64KB / WASM page
)

type Instance struct {
	mod   *wasi.Module
	mem   *memory.Memory
	codec *codec.Codec
	disp  *dispatcher

	scratch int32
}

func NewInstance(size uint) (*Instance, error) {
	if size == 0 {
		size = defaultHeapSize
	}

	i := &Instance{}
	d := &dispatcher{registry: make(map[int32]HostFunc), out: os.Stdout}

	i.mod = wasi.New(NewEnv(d), &WasiStub{})
	i.mem = memory.New(i.mod)
	i.codec = codec.New(i.mem)

	d.mem, d.codec = i.mem, i.codec
	i.disp = d

	i.mod.Xinit_vm(int32(size), int32(maxHostArgs))

	if i.scratch = i.mem.Alloc(codec.ValueSize * maxHostArgs); i.scratch == 0 {
		return nil, memory.ErrGuestOOM
	}
	return i, nil
}

func (i *Instance) Invoke(funcID, argsPtr, numArgs, outPtr int32) {
	i.disp.Invoke(funcID, argsPtr, numArgs, outPtr)
}
func (i *Instance) Stdout(ptr, n int32) {
	print(i.mem.View(ptr, n))
}

// Decode converts a raw mp_obj_t word into a native Go value.
func (i *Instance) Decode(objPtr int32) (any, error) {
	i.mod.Xobj_to_value(objPtr, i.scratch)
	return i.codec.Consume(i.scratch)
}

func (i *Instance) Eval(code string) (any, error) {
	ptr, free, err := i.mem.WriteString(code)
	if err != nil {
		return nil, err
	}
	defer free()

	i.mod.Xeval(ptr, int32(len(code)), i.scratch)
	return i.codec.Consume(i.scratch)
}

// ---------------------------------------------------------------------

type HostFunc func(args []any) (any, error)

func (i *Instance) DefineFunction(name string, fn HostFunc) error {
	if fn == nil {
		return errors.New("nil host func")
	}

	// qstr_from_str strlens this, so it must be NUL-terminated.
	ptr, free, err := i.mem.WriteCString(name)
	if err != nil {
		return err
	}
	defer free()

	id := i.disp.register(fn)
	i.mod.Xdefine_function(ptr, id)
	return nil
}
