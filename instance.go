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

// Instance wraps an internal api.Instance implementation.
type Instance struct {
	in *api.Instance
}

// NewInstance boots an instance of a MicroPython interpreter.
func NewInstance(ctx context.Context, opts ...option) (*Instance, error) {
	opt := newOptions(opts)

	instance, err := api.New()
	if err != nil {
		return nil, err
	}

	for _, g := range opt.globals {
		if err := instance.Set(ctx, g.name, g.value.val); err != nil {
			instance.Close()
			return nil, err
		}
	}

	return &Instance{in: instance}, nil
}

// Set binds a value to a Python global.
func (i *Instance) Set(ctx context.Context, name string, v Value) error {
	if i.in == nil {
		return ErrInstanceNotInitialised
	}
	return i.in.Set(ctx, name, v.val)
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
func (i *Instance) Call(ctx context.Context, name string, args ...Value) (any, error) {
	if i.in == nil {
		return nil, ErrInstanceNotInitialised
	}
	return i.in.Call(ctx, name, unwrapArgs(args)...)
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
