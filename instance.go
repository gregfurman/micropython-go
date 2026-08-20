package micropython

import (
	"context"
	"sync"

	"github.com/gregfurman/micropython-wasi/internal/api"
)

// Instance is a MicroPython interpreter. It is deliberately a narrow view of
// internal/api: what a caller needs to load Python and call it, and no more.
type Instance struct {
	in   *api.Instance
	stop func()
}

// NewInstance boots an interpreter. Cancelling ctx interrupts whatever the
// interpreter is running, which is the only way to stop a guest loop -- there
// is no scheduler or signal inside the module.
func NewInstance(ctx context.Context) (*Instance, error) {
	instance, err := api.New()
	if err != nil {
		return nil, err
	}

	i := &Instance{in: instance, stop: func() {}}
	if ctx.Done() != nil {
		done := make(chan struct{})
		i.stop = sync.OnceFunc(func() { close(done) })

		go func() {
			select {
			case <-ctx.Done():
				instance.Cancel()
			case <-done:
			}
		}()
	}
	return i, nil
}

// Cancel interrupts a call in flight. Safe from any goroutine, and safe when
// nothing is running; the guest sees a KeyboardInterrupt.
func (i *Instance) Cancel() { i.in.Cancel() }

// WithContext runs fn with cancellation wired to ctx, so a guest loop cannot
// outlive it. Returns ctx.Err() if ctx ended first.
func (i *Instance) WithContext(ctx context.Context, fn func() error) error {
	return i.in.WithContext(ctx, fn)
}

// Exec runs src and returns whatever it printed. Use it to define the
// functions a later Bind resolves.
func (i *Instance) Exec(src string) (string, error) {
	return i.in.Exec(src)
}

// Call invokes a Python global by name. For a function called more than once,
// bind it instead: the name lookup then happens once rather than per call.
func (i *Instance) Call(name string, args ...any) (any, error) {
	return i.in.Call(name, args...)
}

func (i *Instance) Close(ctx context.Context) error {
	i.stop()
	return i.in.Close()
}

// Define runs src, which must define name, and returns it as a typed Go
// function. In and Out appear only in the result, so both always have to be
// written out at the call site.
func (i *Instance) Define[In, Out any](name, src string) (func(In) (Out, error), error) {
	return i.in.Define[In, Out](name, src)
}

// Bind is Define for a function that some earlier Exec already defined.
func (i *Instance) Bind[In, Out any](name string) (func(In) (Out, error), error) {
	return i.in.Bind[In, Out](name)
}

// Eval evaluates a single Python expression in a throwaway interpreter.
func Eval(s string) (any, error) {
	instance, err := api.New()
	if err != nil {
		return nil, err
	}
	defer instance.Close()

	return instance.Eval(s)
}
