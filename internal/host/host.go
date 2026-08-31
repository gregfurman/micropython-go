package host

import (
	"fmt"
	"io"

	wasi "github.com/gregfurman/micropython-go/internal/micropython"
)

var _ wasi.Xenv = &Module{}

func (i *Module) Xhost_trampoline(funcID, argsPtr, numArgs, outPtr int32) {
	defer func() {
		if r := recover(); r != nil {
			i.writeErrAt(outPtr, fmt.Errorf("host function panicked: %v", r))
		}
	}()
	if err := i.dispatch(funcID, argsPtr, numArgs, outPtr); err != nil {
		i.writeErrAt(outPtr, err)
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

func (i *Module) Xhost_poll() int32 {
	if i.cancelled.Load() {
		return 1
	}
	return 0
}
