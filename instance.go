package micropython

import (
	"context"
	"errors"

	"github.com/gregfurman/micropython-wasi/internal/api"
	"github.com/gregfurman/micropython-wasi/internal/host"
)

var (
	ErrClosed = host.ErrClosed

	ErrInterrupted = host.ErrInterrupted

	ErrInstanceNotInitialised = errors.New("cannot perform operation on Instance that has not been initialised")
)

// Values that come back out of Python and have no plain Go equivalent. They
// are aliases so a caller can name what Eval and Call already hand them.
type (
	// Exception is the error a failing Eval, Exec or Call returns.
	//
	//	var exc *micropython.Exception
	//	if errors.As(err, &exc) && exc.Type == "KeyError" { ... }
	Exception = host.Exception
)

// Instance wraps an internal api.Instance implementation.
type Instance struct {
	in *api.Instance
}

// NewInstance boots an instance of a MicroPython interpreter.
func NewInstance(ctx context.Context) (*Instance, error) {
	instance, err := api.New()
	if err != nil {
		return nil, err
	}

	return &Instance{in: instance}, nil
}

// Cancel interrupts a call in flight. Safe from any goroutine, and safe when
// nothing is running; the guest sees a KeyboardInterrupt.
func (i *Instance) Cancel() error {
	if i.in == nil {
		return ErrInstanceNotInitialised
	}
	i.in.Cancel()
	return nil
}

// Call invokes a Python global by name and returns its result as a native Go
// value.
func (i *Instance) Call(ctx context.Context, name string, args ...any) (any, error) {
	if i.in == nil {
		return nil, ErrInstanceNotInitialised
	}
	return i.in.Call(ctx, name, args...)
}

// Clone creates an identical copy of the instant. Note, that this will lock the
// underlying interpreter while cloning is taking place.
func (i *Instance) Clone(ctx context.Context) (*Instance, error) {
	if i.in == nil {
		return nil, ErrInstanceNotInitialised
	}

	snap, err := i.in.Snapshot(ctx)
	if err != nil {
		return nil, err
	}

	return fromSnapshot(snap)
}

func (i *Instance) Close() error {
	if i.in == nil {
		return ErrInstanceNotInitialised
	}
	return i.in.Close()
}

func (i *Instance) Eval(ctx context.Context, expr string) (any, error) {
	if i.in == nil {
		return nil, ErrInstanceNotInitialised
	}
	return i.in.Eval(ctx, expr)
}

func (i *Instance) Exec(ctx context.Context, src string) (any, error) {
	if i.in == nil {
		return nil, ErrInstanceNotInitialised
	}
	return i.in.Exec(ctx, src)
}

func (i *Instance) Err() error {
	if i.in == nil {
		return ErrInstanceNotInitialised
	}
	return i.in.Err()
}

func fromSnapshot(s *host.Snapshot) (*Instance, error) {
	instance, err := api.FromSnapshot(s)
	if err != nil {
		return nil, err
	}
	return &Instance{in: instance}, nil
}

func (i *Instance) restore(s *host.Snapshot) error {
	if i.in == nil {
		return ErrInstanceNotInitialised
	}
	return i.in.Restore(context.Background(), s)
}
