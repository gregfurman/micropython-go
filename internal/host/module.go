package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"sync/atomic"

	"github.com/gregfurman/micropython-go/internal/host/codec"
	"github.com/gregfurman/micropython-go/internal/host/env"
	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/host/network"
	"github.com/gregfurman/micropython-go/internal/host/vfs"
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

	// defaultValueArenaCapacity is the region a single guest call gets for its
	// result and everything nested in it. It is a default transfer size, not a
	// semantic maximum.
	defaultValueArenaCapacity = 16 * 1024

	// moduleArenaCapacity is the span the module arena bumps through. One
	// operation takes a result region plus its arguments and the strings that
	// name them, so the span is sized to hold all of that without spilling to
	// the guest heap.
	moduleArenaCapacity = 4 * defaultValueArenaCapacity
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

	arena *memory.Arena

	refs *OwnedReferences

	network       *network.Network
	networkConfig network.Config

	filesystem *vfs.Filesystem

	environment *env.Environment
}

func NewModule(size uint, stdout io.Writer, config network.Config, filesystem fs.FS, vars map[string]string) (*Module, error) {
	if size == 0 {
		size = defaultHeapSize
	}

	i := newModule(stdout, config, filesystem, vars)

	if i.mod.Xinit_vm(int32(size), int32(maxHostArgs)) != 0 {
		return nil, memory.ErrGuestOOM
	}

	arena, err := i.mem.NewArena(moduleArenaCapacity)
	if err != nil {
		return nil, err
	}

	i.arena = arena

	return i, nil
}
func newModule(stdout io.Writer, config network.Config, filesystem fs.FS, vars map[string]string) *Module {
	if stdout == nil {
		stdout = io.Discard
	}

	refs := &OwnedReferences{}

	i := &Module{
		registry: make(map[int32]HostFunc),
		refs:     refs,
		stdout:   stdout,
		shutSig:  util.NewSignaller(),
	}

	mem := memory.New(guestPages, memory.MaxPages)

	i.network = network.New(mem, config)
	i.networkConfig = config
	i.filesystem = vfs.New(mem, filesystem)

	i.environment = env.New(mem, vars)

	i.mem = mem
	i.mod = wasi.New(i, i.network, i.filesystem, i.environment)
	i.mem.Bind(i.mod)

	refs.release = func(id uint32) {
		i.mod.Xrelease_ref(int32(id))
	}

	i.codec = codec.New(i.mem, refs)

	return i
}

func (i *Module) Close() error {
	i.environment.Close()
	return errors.Join(i.network.Close(), i.filesystem.Close())
}

func (i *Module) SetNetworkContext(ctx context.Context) {
	i.network.SetContext(ctx)
}

// ReleasePendingRefs applies queued releases while no guest call is in flight.
// Callers must hold the instance lock. This does not run either collector.
func (i *Module) ReleasePendingRefs() {
	i.refs.Drain()
}

// Begin starts an operation under the instance lock, clearing cancellation and
// releasing references before any arguments are reduced to guest IDs.
//
// Clearing means both halves of the signal: the flag the guest polls, and the
// channel host callbacks wait on. Leaving the channel closed would hand every
// later callback a context that is already done.
func (i *Module) Begin() {
	i.cancelled.Store(false)
	i.shutSig.Reset()
	i.ReleasePendingRefs()
}

func (i *Module) Cancel() {
	i.shutSig.Trigger()
	i.cancelled.Store(true)
}

func (i *Module) Context(ctx context.Context) (context.Context, context.CancelFunc) {
	return i.shutSig.Context(ctx)
}

// result reserves one contiguous region for a guest call to write its result
// into. The C side puts the root value at the front, then allocates all
// payloads from the rest of the region.
//
// It must be called under an arena Mark, which is what releases it.
func (i *Module) result() (int32, error) {
	return i.arena.New(defaultValueArenaCapacity)
}

// consumeArena decodes the tree a guest call left at outPtr. The decoded value
// is fully Go-owned, so the arena can be reset the moment this returns.
func (i *Module) consumeArena(outPtr, used int32) (value.Value, error) {
	if err := transferError(used); err != nil {
		return nil, err
	}
	return i.codec.Decode(outPtr, used)
}

func transferError(used int32) error {
	switch {
	case used > defaultValueArenaCapacity:
		return fmt.Errorf("guest result uses %d bytes, capacity is %d", used, defaultValueArenaCapacity)
	case used >= codec.TransferSize:
		return nil
	case used >= 0:
		return fmt.Errorf("guest result uses %d bytes, want at least %d", used, codec.TransferSize)
	case used == -2:
		return fmt.Errorf("%w: %d byte result region", memory.ErrArenaFull, defaultValueArenaCapacity)
	default:
		return fmt.Errorf("invalid result region: guest returned %d", used)
	}
}

func (i *Module) args(args []any) (int32, error) {
	if len(args) == 0 {
		return 0, nil
	}
	if len(args) > math.MaxInt32/codec.ValueSize {
		return 0, fmt.Errorf("too many arguments: %d", len(args))
	}

	block, err := i.arena.New(int32(len(args)) * codec.ValueSize)
	if err != nil {
		return 0, err
	}
	for n, arg := range args {
		if err := i.codec.EncodeInto(i.arena, block+int32(n)*codec.ValueSize, arg); err != nil {
			return 0, fmt.Errorf("argument %d: %w", n, err)
		}
	}
	return block, nil
}

func (i *Module) Eval(code string) (value.Value, error) {
	reset := i.arena.Mark()
	defer reset()

	codePtr, err := i.arena.String(code)
	if err != nil {
		return nil, err
	}

	outPtr, err := i.arena.New(defaultValueArenaCapacity)
	if err != nil {
		return nil, err
	}

	used := i.mod.Xeval(
		codePtr,
		int32(len(code)),
		outPtr,
		defaultValueArenaCapacity,
	)

	return i.consumeArena(outPtr, used)
}

func (i *Module) Get(name string) (value.Value, error) {
	defer i.arena.Mark()()

	ptr, err := i.arena.String(name)
	if err != nil {
		return nil, err
	}

	outPtr, err := i.result()
	if err != nil {
		return nil, err
	}

	used := i.mod.Xget_global(ptr, int32(len(name)), outPtr, defaultValueArenaCapacity)
	return i.consumeArena(outPtr, used)
}

func (i *Module) Exec(code string) error {
	defer i.arena.Mark()()

	ptr, err := i.arena.String(code)
	if err != nil {
		return err
	}

	outPtr, err := i.result()
	if err != nil {
		return err
	}

	used := i.mod.Xexec(ptr, int32(len(code)), outPtr, defaultValueArenaCapacity)
	_, err = i.consumeArena(outPtr, used)
	return err
}

func (i *Module) Call(name string, args []any) (value.Value, error) {
	defer i.arena.Mark()()

	namePtr, err := i.arena.String(name)
	if err != nil {
		return nil, err
	}

	argsPtr, err := i.args(args)
	if err != nil {
		return nil, err
	}

	outPtr, err := i.result()
	if err != nil {
		return nil, err
	}

	used := i.mod.Xcall(
		namePtr, int32(len(name)), argsPtr, int32(len(args)), outPtr, defaultValueArenaCapacity)

	return i.consumeArena(outPtr, used)
}

// CallRef calls a value the guest handed out as a ref, for callables that no
// global name reaches: a lambda, a bound method, a closure.
func (i *Module) CallRef(obj value.Object, args []any) (value.Value, error) {
	ref, err := i.refs.Lookup(obj.Handle())
	if err != nil {
		return nil, err
	}

	defer i.arena.Mark()()

	argsPtr, err := i.args(args)
	if err != nil {
		return nil, err
	}

	outPtr, err := i.result()
	if err != nil {
		return nil, err
	}

	used := i.mod.Xcall_ref(int32(ref), argsPtr, int32(len(args)), outPtr, defaultValueArenaCapacity)
	return i.consumeArena(outPtr, used)
}

// Resolve re-reads the object a ref names, so a container the host only holds
// a handle to comes back by value.
func (i *Module) Resolve(obj value.Object) (value.Value, error) {
	ref, err := i.refs.Lookup(obj.Handle())
	if err != nil {
		return nil, err
	}

	defer i.arena.Mark()()

	outPtr, err := i.result()
	if err != nil {
		return nil, err
	}

	used := i.mod.Xref_to_value(int32(ref), outPtr, defaultValueArenaCapacity)
	return i.consumeArena(outPtr, used)
}

// Release frees the objects these handles name, giving back every acquisition
// of each rather than one, so the guest gets the memory back now instead of
// whenever the last handle is collected. Handles naming a freed object go
// stale, and report ErrStaleRef if anything uses one afterwards.
func (i *Module) Release(refs ...*value.Ref) {
	for _, ref := range refs {
		i.refs.FreeHandle(ref)
	}
}

// NextGenerator advances a generator ref returned by the guest. When exhausted,
// next returns false. Any uncaught Python exception during iteration is returned
// as an error.
func (i *Module) NextGenerator(
	obj value.Object,
) (value.Value, bool, error) {
	ref, err := i.refs.Lookup(obj.Handle())
	if err != nil {
		return nil, false, err
	}

	reset := i.arena.Mark()
	defer reset()

	outPtr, err := i.result()
	if err != nil {
		return nil, false, err
	}

	used := i.mod.Xiterator_next(
		int32(ref),
		outPtr,
		defaultValueArenaCapacity,
	)

	// Exhaustion is the only outcome that writes nothing. Everything else is an
	// ordinary result, so a raise during iteration arrives as a KIND_EXCEPTION
	// value and comes back out of the decode as the error it is.
	if used == 0 {
		return nil, false, nil
	}

	out, err := i.consumeArena(outPtr, used)
	if err != nil {
		return nil, false, err
	}

	return out, true, nil
}
func (i *Module) Set(name string, v any) error {
	defer i.arena.Mark()()

	namePtr, err := i.arena.String(name)
	if err != nil {
		return err
	}

	valuePtr, err := i.arena.New(codec.ValueSize)
	if err != nil {
		return err
	}
	if err := i.codec.EncodeInto(i.arena, valuePtr, v); err != nil {
		return err
	}

	outPtr, err := i.result()
	if err != nil {
		return err
	}

	used := i.mod.Xset_global(namePtr, int32(len(name)), valuePtr, outPtr, defaultValueArenaCapacity)
	_, err = i.consumeArena(outPtr, used)
	return err
}

func (i *Module) Snapshot() (*Snapshot, error) {
	if i.filesystem.HasOpenFiles() {
		return nil, errors.New("micropython: close open files and directory iterators before cloning or compiling")
	}
	if i.network.HasOpenSockets() {
		return nil, errors.New("micropython: close open sockets before cloning or compiling")
	}

	return &Snapshot{
		memory:     i.mem.Image(),
		stack:      *i.mod.X__stack_pointer(),
		arena:      i.arena.Save(),
		registry:   maps.Clone(i.registry),
		counter:    i.counter,
		stdout:     i.stdout,
		network:    i.networkConfig,
		filesystem: i.filesystem.FS(),
		vars:       i.environment.Vars(),
	}, nil
}

// Restore rewinds guest memory and replaces its reference owner. Old handles
// and their cleanup queues belong to the abandoned owner, never the new one.
func (i *Module) Restore(s *Snapshot) error {
	if err := errors.Join(i.network.Reset(s.network), i.filesystem.Reset(s.filesystem)); err != nil {
		return err
	}
	i.environment.Reset(s.vars)

	i.networkConfig = s.network
	if err := i.mem.Load(s.memory); err != nil {
		return err
	}
	*i.mod.X__stack_pointer() = s.stack

	if i.arena == nil {
		i.arena = &memory.Arena{}
	}
	i.arena.Load(i.mem, s.arena)

	i.mod.Xreset_refs()
	i.refs = &OwnedReferences{release: func(id uint32) { i.mod.Xrelease_ref(int32(id)) }}
	i.codec = codec.New(i.mem, i.refs)

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

	defer i.arena.Mark()()

	// qstr_from_str strlens this, so it must be NUL-terminated.
	ptr, err := i.arena.CString(name)
	if err != nil {
		return err
	}

	i.mod.Xdefine_function(ptr, i.register(fn))
	return nil
}
