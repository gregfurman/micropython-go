package host

import (
	"fmt"
	"io"

	wasi "github.com/gregfurman/micropython-go/internal/micropython"
)

var _ wasi.Xenv = &Module{}

func (i *Module) Xmemory() wasi.Memory {
	return i.mem
}

func (i *Module) Xhost_trampoline(funcID, argsPtr, argsSize, outPtr, outCapacity int32) {
	defer func() {
		if r := recover(); r != nil {
			i.writeErr(outPtr, outCapacity, fmt.Errorf("host function panicked: %v", r))
		}
	}()

	if err := i.dispatch(funcID, argsPtr, argsSize, outPtr, outCapacity); err != nil {
		i.writeErr(outPtr, outCapacity, err)
	}
}

func (i *Module) Xhost_stdout(ptr, n int32) {
	if i.stdout == io.Discard {
		return
	}

	b, err := i.mem.View(ptr, n)
	if err != nil {
		return
	}

	_, _ = i.stdout.Write(b)
}

// Xgo_ref_add and Xgo_ref_free are the guest asking the host to do the
// reference bookkeeping it no longer keeps for itself. Both run while a guest
// call is in flight, so the instance lock is already held by this goroutine and
// neither may reach for it.
func (i *Module) Xgo_ref_add(addr int32) int32 {
	return i.refs.Acquire(uint32(addr))
}

func (i *Module) Xgo_ref_free(id int32) int32 {
	// The C caller removes the pin when this was the last acquisition.
	// Release would enter the guest again and unpin the same slot twice.
	if i.refs.drop(uint32(id)) {
		return 1
	}

	return 0
}

func (i *Module) Xhost_poll() int32 {
	if i.cancelled.Load() {
		return 1
	}

	return 0
}
