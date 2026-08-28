package host

import (
	"fmt"
	"io"
	"maps"
	"sync/atomic"

	"github.com/gregfurman/micropython-go/internal/host/codec"
	"github.com/gregfurman/micropython-go/internal/host/memory"
)

// TODO: this should probably be folded into the Module approach

type dispatcher struct {
	mem   *memory.Memory
	codec *codec.Codec

	registry map[int32]HostFunc
	counter  int32

	out       io.Writer
	cancelled atomic.Bool
}

func (d *dispatcher) Poll() int32 {
	if d.cancelled.Load() {
		return 1
	}
	return 0
}

func (d *dispatcher) Invoke(funcID, argsPtr, numArgs, outPtr int32) {
	defer func() {
		if r := recover(); r != nil {
			d.writeErrAt(outPtr, fmt.Errorf("host function panicked: %v", r))
		}
	}()
	if err := d.dispatch(funcID, argsPtr, numArgs, outPtr); err != nil {
		d.writeErrAt(outPtr, err)
	}
}

func (d *dispatcher) Stdout(ptr, n int32) {
	b, err := d.mem.View(ptr, n)
	if err != nil {
		return
	}
	d.out.Write(b)
}

func (d *dispatcher) dispatch(funcID, argsPtr, numArgs, outPtr int32) error {
	fn, ok := d.registry[funcID]
	if !ok {
		return fmt.Errorf("unknown host func %d", funcID)
	}
	if numArgs < 0 || numArgs > maxHostArgs {
		return fmt.Errorf("bad arg count %d (max %d)", numArgs, maxHostArgs)
	}
	if _, err := d.mem.View(argsPtr, numArgs*codec.ValueSize); err != nil {
		return fmt.Errorf("args block: %w", err)
	}

	if _, err := d.mem.View(outPtr, codec.ValueSize); err != nil {
		return fmt.Errorf("return slot: %w", err)
	}

	if err := d.codec.Encode(outPtr, nil); err != nil {
		return fmt.Errorf("return slot: %w", err)
	}

	args := make([]any, numArgs)
	for k := range numArgs {
		v, err := d.codec.Consume(argsPtr + k*codec.ValueSize)
		if err != nil {
			for rest := k + 1; rest < numArgs; rest++ {
				_, _ = d.codec.Consume(argsPtr + rest*codec.ValueSize)
			}
			return fmt.Errorf("arg %d: %w", k, err)
		}
		args[k] = v
	}

	out, err := fn(args)
	if err != nil {
		return err
	}
	return d.codec.Encode(outPtr, out)
}

func (d *dispatcher) register(fn HostFunc) int32 {
	d.counter++
	d.registry[d.counter] = fn
	return d.counter
}

// restore rebinds the registry to the one a snapshot captured. Guest memory
// refers to host functions by id, so restoring memory without also restoring
// the registry would leave those ids dangling.
func (d *dispatcher) restore(registry map[int32]HostFunc, counter int32) {
	d.registry = maps.Clone(registry)
	if d.registry == nil {
		d.registry = make(map[int32]HostFunc)
	}
	d.counter = counter
}

func (d *dispatcher) writeErrAt(ptr int32, err error) {
	if e := d.codec.EncodeError(ptr, err); e != nil {
		// fallback to just raise a regular Exception
		_ = d.codec.EncodeEmptyError(ptr)
	}
}
