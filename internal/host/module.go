package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"sync/atomic"

	"github.com/gregfurman/micropython-go/internal/host/codec"
	"github.com/gregfurman/micropython-go/internal/host/memory"
	wasi "github.com/gregfurman/micropython-go/internal/micropython"
	"github.com/gregfurman/micropython-go/internal/util"
	"github.com/gregfurman/micropython-go/internal/value"
)

const (
	maxHostArgs = 8 // TODO: investigate + potentially autogen with cgo def gen

	// guestPages is the memory the module is linked against, matching
	// -Wl,--import-memory -Wl,--initial-memory in build/build.sh. The host
	// hands this in, so it has to cover the guest's stack and data segments.
	guestPages = 393216 / memory.PageSize

	defaultHeapSize = 2 * memory.PageSize // give 128KB to start
)

type Module struct {
	mod *wasi.Module
	mem *memory.Memory

	codec *codec.Codec

	registry map[int32]HostFunc
	counter  int32

	cancelled atomic.Bool
	shutSig   *util.Signaller

	stdout io.Writer

	scratch     int32
	walkScratch []int32

	refs *OwnedReferences
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
	refs := &OwnedReferences{
		pending: make(chan refKey, pendingRefs),
	}

	i := &Module{
		registry: make(map[int32]HostFunc),
		refs:     refs,
		stdout:   stdout,
		shutSig:  util.NewSignaller(),
	}

	i.mem = memory.New(guestPages, memory.MaxPages)
	i.mod = wasi.New(i)
	i.mem.Bind(i.mod)
	i.codec = codec.New(i.mem, refs)

	return i
}

// GC frees the refs the host has dropped since the last operation. Callers
// serialise access to a Module and run this before touching the guest, the only
// place it is safe to release one.
func (i *Module) GC() {
	i.refs.Drain(func(id uint32) { i.mod.Xrelease_ref(int32(id)) })
}

// Begin starts an operation, clearing a cancel left over from the last one.
func (i *Module) Begin() {
	i.cancelled.Store(false)
}

func (i *Module) Cancel() {
	i.shutSig.Trigger()
	i.cancelled.Store(true)
}

func (i *Module) Context(ctx context.Context) (context.Context, context.CancelFunc) {
	return i.shutSig.Context(ctx)
}

func (i *Module) Eval(code string) (value.Value, error) {
	ptr, free, err := i.mem.WriteString(code)
	if err != nil {
		return nil, err
	}
	defer free()

	i.mod.Xeval(ptr, int32(len(code)), i.scratch)
	return i.consume(i.scratch)
}

func (i *Module) Get(name string) (value.Value, error) {
	ptr, free, err := i.mem.WriteString(name)
	if err != nil {
		return nil, err
	}
	defer free()

	i.mod.Xget_global(ptr, int32(len(name)), i.scratch)
	return i.consume(i.scratch)
}

func (i *Module) Exec(code string) error {
	ptr, free, err := i.mem.WriteString(code)
	if err != nil {
		return err
	}

	defer free()

	i.mod.Xexec(ptr, int32(len(code)), i.scratch)
	_, err = i.consume(i.scratch)
	return err
}

func (i *Module) Call(name string, args []any) (value.Value, error) {
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
		// Handed over: the guest releases the block, the raise path included.
		defer func() { encoded = -1 }()
	}

	i.mod.Xcall(namePtr, int32(len(name)), argsPtr, int32(len(args)), i.scratch)
	return i.consume(i.scratch)
}

// CallRef calls a value the guest handed out as a ref, for callables that no
// global name reaches: a lambda, a bound method, a closure.
func (i *Module) CallRef(obj value.Object, args []any) (value.Value, error) {
	ref, err := i.refs.Lookup(obj.Handle())
	if err != nil {
		return nil, err
	}

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
		// Handing the block over transfers it: the guest releases every child
		// allocation once the call is over, the raise path included.
		defer func() { encoded = -1 }()
	}

	i.mod.Xcall_ref(int32(ref), argsPtr, int32(len(args)), i.scratch)
	return i.consume(i.scratch)
}

// Resolve re-reads the object a ref names, so a container the host only holds
// a handle to comes back by value.
func (i *Module) Resolve(obj value.Object) (value.Value, error) {
	ref, err := i.refs.Lookup(obj.Handle())
	if err != nil {
		return nil, err
	}

	i.mod.Xref_to_value(int32(ref), i.scratch)
	return i.consume(i.scratch)
}

// NextGenerator advances a generator ref returned by the guest. When exhausted,
// next returns false. Any uncaught Python exception during iteration is returned
// as an error.
func (i *Module) NextGenerator(obj value.Object) (out value.Value, next bool, err error) {
	ref, err := i.refs.Lookup(obj.Handle())
	if err != nil {
		return nil, false, err
	}

	status, err := i.iterate(int32(ref), i.scratch)
	if err != nil || status == 0 {
		return nil, false, err
	}

	out, err = i.consume(i.scratch)
	return out, err == nil, err
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
	_, err = i.consume(i.scratch)
	return err
}

func (i *Module) Snapshot() *Snapshot {
	return &Snapshot{
		memory:   i.mem.Image(),
		stack:    *i.mod.X__stack_pointer(),
		scratch:  i.scratch,
		walk:     slices.Clone(i.walkScratch),
		registry: maps.Clone(i.registry),
		counter:  i.counter,
		stdout:   i.stdout,
	}
}

// Restore rewinds guest memory, so every ref minted before it now names a
// different object. Bumping the epoch invalidates those handles and discards
// the releases they queued.
func (i *Module) Restore(s *Snapshot) error {
	i.refs.Inc()

	if err := i.mem.Load(s.memory); err != nil {
		return err
	}
	*i.mod.X__stack_pointer() = s.stack
	i.scratch = s.scratch
	i.walkScratch = slices.Clone(s.walk)

	// NOTE: Invalidate refs
	i.mod.Xreset_refs()

	i.restore(s.registry, s.counter)
	i.cancelled.Store(false)
	return nil
}

// ---------------------------------------------------------------------

type HostFunc func(ctx context.Context, args []value.Value) (value.Value, error)

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

	i.mod.Xdefine_function(ptr, i.register(fn))
	return nil
}
