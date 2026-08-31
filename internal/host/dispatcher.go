package host

import (
	"fmt"
	"maps"

	"github.com/gregfurman/micropython-go/internal/host/codec"
)

func (i *Module) dispatch(funcID, argsPtr, numArgs, outPtr int32) error {
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

	if _, err := i.mem.View(outPtr, codec.ValueSize); err != nil {
		return fmt.Errorf("return slot: %w", err)
	}

	if err := i.codec.Encode(outPtr, nil); err != nil {
		return fmt.Errorf("return slot: %w", err)
	}

	args := make([]any, numArgs)
	for k := range numArgs {
		v, err := i.codec.Consume(argsPtr + k*codec.ValueSize)
		if err != nil {
			for rest := k + 1; rest < numArgs; rest++ {
				_, _ = i.codec.Consume(argsPtr + rest*codec.ValueSize)
			}
			return fmt.Errorf("arg %d: %w", k, err)
		}
		args[k] = v
	}

	out, err := fn(args)
	if err != nil {
		return err
	}
	return i.codec.Encode(outPtr, out)
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

func (i *Module) writeErrAt(ptr int32, err error) {
	if e := i.codec.EncodeError(ptr, err); e != nil {
		// fallback to just raise a regular Exception
		_ = i.codec.EncodeEmptyError(ptr)
	}
}
