package impl

import (
	"fmt"
	"io"

	"github.com/gregfurman/micropython-go/internal/minimal/impl/codec"
	"github.com/gregfurman/micropython-go/internal/minimal/impl/memory"
)

type dispatcher struct {
	mem   *memory.Memory
	codec *codec.Codec

	registry map[int32]HostFunc
	counter  int32

	out io.Writer
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

	args := make([]any, numArgs)
	for k := range numArgs {
		v, err := d.codec.Borrow(argsPtr + k*codec.ValueSize) // guest owns these
		if err != nil {
			return fmt.Errorf("arg %d: %w", k, err)
		}
		args[k] = v
	}

	res, err := fn(args)
	if err != nil {
		return err
	}
	return d.codec.Encode(outPtr, res)
}

func (d *dispatcher) register(fn HostFunc) int32 {
	d.counter++
	d.registry[d.counter] = fn
	return d.counter
}

func (d *dispatcher) writeErrAt(ptr int32, err error) {
	if e := d.codec.EncodeError(ptr, err); e != nil {
		// fallback to just raise a regular Exception
		_ = d.codec.EncodeEmptyError(ptr)
	}
}
