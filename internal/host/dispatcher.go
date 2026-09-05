package host

import (
	"context"
	"fmt"
	"maps"

	"github.com/gregfurman/micropython-go/internal/host/codec"
	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/value"
)

func (i *Module) dispatch(funcID, argsPtr, numArgs, outPtr, outCapacity int32) error {
	fn, ok := i.registry[funcID]
	if !ok {
		return fmt.Errorf("unknown host func %d", funcID)
	}
	if numArgs < 0 || numArgs > maxHostArgs {
		return fmt.Errorf("bad arg count %d (max %d)", numArgs, maxHostArgs)
	}
	if _, err := i.mem.View(argsPtr, numArgs*codec.ValueSize); err != nil {
		return fmt.Errorf("args block: %w", err)
	}

	args := make([]value.Value, numArgs)
	for k := range numArgs {
		v, err := i.codec.Consume(argsPtr + k*codec.ValueSize)
		if err != nil {
			return fmt.Errorf("arg %d: %w", k, err)
		}
		args[k] = v
	}

	ctx, cancel :=
		i.Context(context.Background())
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
