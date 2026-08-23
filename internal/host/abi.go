package host

import (
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"sync/atomic"

	wasi "github.com/gregfurman/micropython-wasi/internal/micropython"
)

// ABI is the crossing between Go and MicroPython, split by direction: it
// implements the module's val_* imports (decode.go), encodes arguments for it
// (encode.go), and calls its exports (exports.go). This file is what those
// share.
//
// An ABI is not safe for concurrent use. The module is one linear memory and
// one call scribbles over another's scratch, so exactly one of Eval, Call and
// Func may be in flight at a time; the caller owns that exclusion. Cancel,
// Close, Closed and Xpoll are the exceptions and are safe from any goroutine.
type ABI struct {
	mod *wasi.Module

	// Where the shadow stack ends and the rest of linear memory begins; see
	// newABI. Fixed for the life of the module.
	base int32

	dec decoder
	enc encoder

	// Host functions the guest can call: the registry ids resolve against, the
	// decodes call_begin put aside, and the reply call_end hands back. See
	// funcs.go.
	funcs *Registry
	saved []decoder
	rep   encoder

	// Which generation of object references is live. Bumped at the start of
	// every call, so a reference from an earlier one is refused rather than
	// resolved against whatever now sits at that index. Values are unique
	// across every ABI, so matching epochs also means matching interpreter.
	epoch uint64

	state atomic.Int32
	trap  atomic.Pointer[TrapError]

	cancelled atomic.Bool
}

const (
	stateOpen int32 = iota
	stateClosed
	stateDead
)

var (
	ErrClosed      = errors.New("micropython: instance is closed")
	ErrInterrupted = errors.New("micropython: call was interrupted")
)

type TrapError struct {
	Value any
	Stack []byte
}

func (e *TrapError) Error() string { return fmt.Sprintf("micropython: trap: %v", e.Value) }

var _ wasi.Xhost = (*ABI)(nil)

func (a *ABI) status() error {
	switch a.state.Load() {
	case stateClosed:
		return ErrClosed
	case stateDead:
		if t := a.trap.Load(); t != nil {
			return t
		}
		return ErrClosed
	}
	return nil
}

// Begin readies the ABI for one call and reports whether it may proceed.
//
// The caller holds the instance lock and must call Begin before arming any
// cancellation. Clearing the stale request here, rather than inside the call,
// is what makes Cancel unambiguous: context.AfterFunc on an already-expired
// context fires immediately, and a clear that happened later would erase the
// very request it was armed for.
func (a *ABI) Begin() error {
	if err := a.status(); err != nil {
		return err
	}
	a.cancelled.Store(false)
	return nil
}

// Cancel asks a running call to stop. Safe from any goroutine, and safe when
// nothing is running; the guest sees a KeyboardInterrupt.
//
// It is best effort and cooperative. The request lands at the next VM hook, so
// a guest sitting in one long C-level operation -- a regex match, a big-int
// multiply, a sort -- does not stop until that operation finishes.
//
// The request is also sticky for the rest of the call, since Xpoll keeps
// reporting it: a bare `except KeyboardInterrupt` in the guest cannot swallow a
// cancellation. The cost is that `finally` blocks are interrupted too, so guest
// cleanup does not run.
func (a *ABI) Cancel() {
	a.cancelled.Store(true)
}

func (a *ABI) Close() {
	a.Cancel()
	a.state.CompareAndSwap(stateOpen, stateClosed)
}

func (a *ABI) Closed() bool { return a.state.Load() != stateOpen }

func (a *ABI) Release() {
	a.mod = nil
	a.dec.reset()
	a.enc.reset()
}

// Xpoll is called by the VM hook every MICROPY_VM_HOOK_COUNT bytecodes, on the
// guest's own goroutine. Non-zero makes the guest raise KeyboardInterrupt.
func (a *ABI) Xpoll() int32 {
	if a.cancelled.Load() || a.state.Load() != stateOpen {
		return 1
	}
	return 0
}

func (a *ABI) guard(err *error) {
	r := recover()
	if r == nil {
		return
	}
	trap := &TrapError{Value: r, Stack: debug.Stack()}
	a.trap.CompareAndSwap(nil, trap)
	a.state.Store(stateDead)
	*err = trap
}

func (a *ABI) mem() []byte {
	if a.mod == nil {
		return nil
	}
	return *a.mod.Xmemory().Slice()
}

func (a *ABI) slice(ptr, length int32) ([]byte, bool) {
	if ptr < 0 || length < 0 {
		return nil, false
	}
	mem := a.mem()
	end := int64(ptr) + int64(length)
	if end > int64(len(mem)) {
		return nil, false
	}
	return mem[ptr:end], true
}

func (a *ABI) str(ptr, length int32) string {
	b, ok := a.slice(ptr, length)
	if !ok {
		return ""
	}
	return string(b)
}

func (a *ABI) Write(b []byte) (int32, error) { return a.write(b) }

func (a *ABI) WriteString(s string) (int32, error) { return a.write(s) }

func (a *ABI) write[T ~[]byte | ~string](b T) (ptr int32, err error) {
	defer a.guard(&err)

	if int64(len(b)) > math.MaxInt32 {
		return 0, fmt.Errorf("micropython: %d bytes is too large to pass", len(b))
	}
	n := int32(len(b))

	ptr = a.mod.Xmp_api_scratch(n)
	if ptr <= 0 {
		if n == 0 {
			return 0, nil
		}
		return 0, fmt.Errorf("micropython: out of memory reserving %d bytes", n)
	}

	dst, ok := a.slice(ptr, n)
	if !ok {
		return 0, fmt.Errorf("micropython: scratch at %d is short of %d bytes", ptr, n)
	}
	copy(dst, b)
	return ptr, nil
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

func (a *ABI) Err() error {
	switch a.state.Load() {
	case stateClosed:
		return ErrClosed
	case stateDead:
		if t := a.trap.Load(); t != nil {
			return t
		}
		return ErrClosed
	}
	return nil
}
