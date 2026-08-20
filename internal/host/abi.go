package host

import (
	"fmt"
	"sync/atomic"

	wasi "github.com/gregfurman/micropython-wasi/internal/micropython"
)

// ABI is the crossing between Go and MicroPython, split by direction: it
// implements the module's val_* imports (decode.go), encodes arguments for it
// (encode.go), and calls its exports (exports.go). This file is what those
// share.
type ABI struct {
	mod *wasi.Module

	dec decoder
	enc encoder

	staging []byte

	// Read by the module through Xpoll while it runs, so it must be settable
	// without holding whatever lock the caller took to start the call.
	cancelled atomic.Bool
}

var _ wasi.Xhost = (*ABI)(nil)

// Cancel asks a running call to stop. Safe from any goroutine, and safe when
// nothing is running. The guest sees a KeyboardInterrupt, so guest code can
// observe it with `try:`; the host sees an ordinary error.
func (a *ABI) Cancel() { a.cancelled.Store(true) }

// Xpoll is called by the VM hook every MICROPY_VM_HOOK_COUNT instructions.
func (a *ABI) Xpoll() int32 {
	if a.cancelled.Load() {
		return 1
	}
	return 0
}

// begin clears a cancellation left over from a previous call.
func (a *ABI) begin() { a.cancelled.Store(false) }

// Write copies b into the module's scratch buffer and returns its offset. The
// module copies whatever it reads out of scratch before returning, so one
// buffer serves every crossing.
func (a *ABI) Write(b []byte) (int32, error) {
	ptr := a.mod.Xmp_api_scratch(int32(len(b)))
	if ptr == 0 && len(b) > 0 {
		return 0, fmt.Errorf("micropython: out of memory reserving %d bytes", len(b))
	}
	copy(a.mem()[ptr:], b)
	return ptr, nil
}

func (a *ABI) ReadString(ptr, length int32) string { return a.str(ptr, length) }

func (a *ABI) mem() []byte { return *a.mod.Xmemory().Slice() }

func (a *ABI) str(ptr, length int32) string {
	if ptr == 0 || length <= 0 {
		return ""
	}
	return string(a.mem()[ptr : ptr+length])
}

func (a *ABI) WriteString(s string) (int32, error) {
	a.staging = append(a.staging[:0], s...)
	return a.Write(a.staging)
}

// WriteArgs encodes values into the module's scratch buffer, returning its
// offset and length.
func (a *ABI) WriteArgs(values []any) (ptr, n int32, err error) {
	a.enc.reset()
	for _, v := range values {
		if err := a.enc.value(v, 0); err != nil {
			return 0, 0, err
		}
	}

	ptr, err = a.Write(a.enc.buf)
	if err != nil {
		return 0, 0, err
	}
	return ptr, int32(len(a.enc.buf)), nil
}
