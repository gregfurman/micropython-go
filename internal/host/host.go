package host

import (
	"fmt"

	wasi "github.com/gregfurman/micropython-go/internal/micropython"
)

var _ wasi.Xenv = &Instance{}

func (i *Instance) Xhost_trampoline(funcID, argsPtr, numArgs, outPtr int32) {
	defer func() {
		if r := recover(); r != nil {
			i.writeErrAt(outPtr, fmt.Errorf("host function panicked: %v", r))
		}
	}()
	if err := i.dispatch(funcID, argsPtr, numArgs, outPtr); err != nil {
		i.writeErrAt(outPtr, err)
	}
}

func (i *Instance) Xhost_stdout(ptr, n int32) {
	b, err := i.mem.View(ptr, n)
	if err != nil {
		return
	}

	_, _ = i.buf.Write(b)
}

func (i *Instance) Xhost_poll() int32 {
	if i.cancelled.Load() {
		return 1
	}
	return 0
}

// func (i *Instance) Init(m any) {
// 	// This really should panic since it means we're unable to set
// 	// the wasi.Module.
// 	i.mod = m.(*wasi.Module)
// }
