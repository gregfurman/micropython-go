// Package api embeds MicroPython.
//
// It is layered on internal/host: ABI owns the module and does the crossing,
// Instance is the API on top of it. A MicroPython value is an mp_obj_t, a
// pointer into a garbage-collected heap that Go cannot keep rooted, so results
// are streamed back through val_* callbacks and arguments are encoded into a
// buffer the module decodes onto a stack its own GC traces.
package api

import (
	"context"
	"errors"
	"sync"

	"github.com/gregfurman/micropython-wasi/internal/host"
	"github.com/gregfurman/micropython-wasi/internal/value"
)

var (
	errInvalidType = errors.New("invalid type evaluated")

	// ErrClosed is returned by every operation on a closed Instance.
	ErrClosed = errors.New("micropython: instance is closed")

	// ErrStale is returned when a Func outlives the interpreter it was
	// resolved against, which Reset and Close both end.
	ErrStale = errors.New("micropython: function belongs to a superseded interpreter")
)

type Instance struct {
	mu  sync.Mutex
	abi *host.ABI

	// Guards abi alone, so Cancel and Close can reach it while a call holds mu.
	abiMu sync.Mutex

	gen uint64
}

func New() (*Instance, error) {
	return &Instance{abi: host.New()}, nil
}

func (i *Instance) setABI(abi *host.ABI) {
	i.abiMu.Lock()
	defer i.abiMu.Unlock()
	i.abi = abi
}

// Close releases the interpreter. If a call is in flight on another goroutine
// it is interrupted first: taking the lock would otherwise mean waiting for
// the very call being cancelled.
func (i *Instance) Close() error {
	if abi := i.current(); abi != nil {
		abi.Cancel()
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	i.setABI(nil)
	i.gen++
	return nil
}

// Cancel interrupts a call in flight, without ending the interpreter. The
// running Python sees a KeyboardInterrupt.
func (i *Instance) Cancel() {
	if abi := i.current(); abi != nil {
		abi.Cancel()
	}
}

// current reads the ABI without the main lock, which a cancelling goroutine
// cannot take while the call it wants to stop is holding it.
func (i *Instance) current() *host.ABI {
	i.abiMu.Lock()
	defer i.abiMu.Unlock()
	return i.abi
}

// WithContext runs fn with cancellation wired to ctx, so a guest loop cannot
// outlive it.
func (i *Instance) WithContext(ctx context.Context, fn func() error) error {
	if ctx.Done() == nil {
		return fn()
	}

	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			i.Cancel()
		case <-done:
		}
	}()

	err := fn()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func (i *Instance) Reset() error {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.abi == nil {
		return ErrClosed
	}
	i.setABI(host.New())
	i.gen++

	return nil
}

// Exec runs src as a script and returns whatever it printed.
func (i *Instance) Exec(src string) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.abi == nil {
		return "", ErrClosed
	}
	if err := i.abi.Eval(src, host.ModeExec); err != nil {
		return "", err
	}
	return i.abi.Output(), nil
}

// Eval evaluates a single expression and returns the result as a native Go
// value:
//
//	None            nil
//	bool            bool
//	int             int64
//	float           float64
//	str             string
//	bytes           []byte
//	list            []any
//	tuple           Tuple
//	dict            map[string]any, or map[any]any for non-string keys
//	anything else   Object{Type, Repr}
func (i *Instance) Eval(expr string) (any, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.abi == nil {
		return nil, ErrClosed
	}
	if err := i.abi.Eval(expr, host.ModeValue); err != nil {
		return nil, err
	}
	return i.abi.Value()
}

// TypedEval is a type safe version of Eval.
func (i *Instance) TypedEval[T any](expr string) (T, error) {
	var zero T
	res, err := i.Eval(expr)
	if err != nil {
		return zero, err
	}

	return value.Coerce[T](res)
}

// Call invokes a Python global by name. For a function called more than once,
// resolve it with Func or Define instead and call that: the name lookup then
// happens once rather than per call.
func (i *Instance) Call(name string, args ...any) (any, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.abi == nil {
		return nil, ErrClosed
	}
	handle, err := i.abi.Func(name)
	if err != nil {
		return nil, err
	}
	return i.abi.Call(handle, args)
}

// Output returns what the last call printed.
func (i *Instance) Output() string {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.abi == nil {
		return ""
	}
	return i.abi.Output()
}
