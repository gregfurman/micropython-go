package host

import (
	"context"
	"fmt"
	"maps"

	"github.com/gregfurman/micropython-go/internal/host/codec"
	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/value"
)

// callbackArenaCapacity mirrors HOST_CALLBACK_ARENA_CAPACITY in build/hostfn.c:
// the scratch span the guest lends a callback for its arguments. It is a
// separate span with a separate owner from the result region, so it gets its
// own bound rather than borrowing defaultValueArenaCapacity, which the two
// happen to share today.
const callbackArenaCapacity = 16 * 1024

// callbackArgsError checks the span the guest says it wrote its arguments into.
// A transfer is at least a header, since that is what the region opens with.
func callbackArgsError(size int32) error {
	switch {
	case size > callbackArenaCapacity:
		return fmt.Errorf("callback arguments use %d bytes, capacity is %d", size, callbackArenaCapacity)
	case size >= codec.TransferSize:
		return nil
	default:
		return fmt.Errorf("callback arguments use %d bytes, want at least %d", size, codec.TransferSize)
	}
}

func (i *Module) dispatch(funcID, argsPtr, argsSize, outPtr, outCapacity int32) error {
	if err := callbackArgsError(argsSize); err != nil {
		return err
	}
	fn, ok := i.registry[funcID]
	if !ok {
		// The arguments still have to be given back, but the rejection is what
		// the caller needs to hear: a ledger that will not parse is a symptom
		// of the same call, not a second failure worth reporting instead.
		if err := i.codec.ReleaseRefs(argsPtr, argsSize); err != nil {
			return fmt.Errorf("unknown host func %d (releasing arguments: %w)", funcID, err)
		}
		return fmt.Errorf("unknown host func %d", funcID)
	}

	decoded, err := i.codec.Decode(argsPtr, argsSize)
	if err != nil {
		return fmt.Errorf("callback arguments: %w", err)
	}
	args, ok := decoded.(value.TupleValue)
	if !ok || len(args) > maxHostArgs {
		return fmt.Errorf("invalid callback argument tuple (max %d arguments)", maxHostArgs)
	}

	ctx, cancel := i.Context(context.Background())
	defer cancel()

	out, err := fn(ctx, args)
	if err != nil {
		return err
	}

	arena, root, err := i.returnArena(outPtr, outCapacity)
	if err != nil {
		return err
	}
	return i.codec.EncodeInto(arena, root, out)
}

// returnArena views the region the guest lends a host callback for its return
// value. The guest owns the span and reclaims it when the trampoline returns,
// so the arena never spills to the heap: the whole tree has to fit.
//
// The root record sits at the front, matching what the C side reads back, and
// is reserved first so nested payloads land after it.
func (i *Module) returnArena(outPtr, outCapacity int32) (*memory.Arena, int32, error) {
	if outCapacity < codec.ValueSize {
		return nil, 0, fmt.Errorf("return arena too small: %d", outCapacity)
	}
	if _, err := i.mem.View(outPtr, outCapacity); err != nil {
		return nil, 0, fmt.Errorf("return arena: %w", err)
	}

	arena := i.mem.ArenaAt(outPtr, outCapacity)
	root, err := arena.New(codec.ValueSize)
	if err != nil {
		return nil, 0, err
	}
	return arena, root, nil
}

func (i *Module) register(fn HostFunc) int32 {
	i.counter++
	i.registry[i.counter] = fn
	return i.counter
}

// restore rebinds the registry to the one a snapshot captured. Guest memory
// refers to host functions by id, so restoring memory without also restoring
// the registry would leave those ids dangling.
func (i *Module) restore(registry map[int32]HostFunc, counter int32) {
	i.registry = maps.Clone(registry)
	if i.registry == nil {
		i.registry = make(map[int32]HostFunc)
	}
	i.counter = counter
}

// writeErr replaces whatever a failed callback left in the return region with
// the exception to raise. Each attempt takes a fresh view of the region, which
// rewinds the bump pointer the way output_arena_reset does on the C side.
func (i *Module) writeErr(outPtr, outCapacity int32, err error) {
	arena, root, aerr := i.returnArena(outPtr, outCapacity)
	if aerr != nil {
		return
	}
	if e := i.codec.EncodeErrorInto(arena, root, err); e == nil {
		return
	}

	// The text did not fit. Fall back to a bare exception, which always does.
	arena, root, aerr = i.returnArena(outPtr, outCapacity)
	if aerr != nil {
		return
	}
	_ = i.codec.EncodeEmptyErrorInto(arena, root)
}
