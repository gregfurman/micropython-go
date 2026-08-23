package api

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/gregfurman/micropython-wasi/internal/host"
	"github.com/gregfurman/micropython-wasi/internal/value"
)

// Instance is one MicroPython interpreter.
//
// It is safe to use from several goroutines but it is not concurrent: the guest
// is a single linear memory, so calls queue behind each other. Code that wants
// parallelism wants more instances, not more callers -- they are cheap here,
// since the module is compiled Go and starting one is an allocation plus
// mp_init, with no runtime to spin up.
type Instance struct {
	lock chan struct{}
	abi  atomic.Pointer[host.ABI]
}

func New() (*Instance, error) {
	abi, err := host.New()
	if err != nil {
		return nil, err
	}

	i := &Instance{lock: make(chan struct{}, 1)}
	i.abi.Store(abi)
	return i, nil
}

// Exec runs src as a script and returns whatever is printed to stdout.
func (i *Instance) Exec(ctx context.Context, src string) (string, error) {
	var out string
	err := i.run(ctx, func(abi *host.ABI) error {
		evalErr := abi.Eval(src, host.ModeExec)

		var trap *host.TrapError
		if !errors.As(evalErr, &trap) {
			out = abi.Output()
		}
		return evalErr
	})
	return out, err
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
func (i *Instance) Eval(ctx context.Context, expr string) (any, error) {
	var out any
	err := i.run(ctx, func(abi *host.ABI) error {
		if err := abi.Eval(expr, host.ModeValue); err != nil {
			return err
		}

		var err error
		out, err = abi.Value()
		return err
	})
	return out, err
}

// Call invokes a Python global by name.
func (i *Instance) Call(ctx context.Context, name string, args ...any) (any, error) {
	var out any
	err := i.run(ctx, func(abi *host.ABI) error {
		handle, err := abi.Func(name)
		if err != nil {
			return err
		}
		out, err = abi.Call(handle, args)
		return err
	})
	return out, err
}

// Set binds a value to a Python global, without going through source text.
func (i *Instance) Set(ctx context.Context, name string, v value.Value) error {
	return i.run(ctx, func(abi *host.ABI) error {
		return abi.Set(name, v)
	})
}

// ------------------------------------------------------------------------

func (i *Instance) Cancel() {
	i.abi.Load().Cancel()
}

// Err reports whether the interpreter is still usable: nil, or the trap that
// killed it, or ErrClosed. A trapped instance recovers only through Reset or
// Restore.
func (i *Instance) Err() error {
	return i.abi.Load().Err()
}

func (i *Instance) Close() error {
	i.abi.Load().Close()

	i.lock <- struct{}{}
	defer i.release()

	abi := i.abi.Load()
	abi.Close()
	abi.Release()

	return nil
}

// ------------------------------------------------------------------------

// Snapshot freezes the interpreter as it stands, so it can be restored into
// this instance or handed to new ones. It takes the instance for the duration,
// so it cannot run while a call is in flight.
func (i *Instance) Snapshot(ctx context.Context) (*host.Snapshot, error) {
	if err := i.acquire(ctx); err != nil {
		return nil, err
	}
	defer i.release()

	return i.abi.Load().Snapshot()
}

// Reset replaces the interpreter with a fresh one, discarding every global and
// definition, and invalidating any Func resolved before it.
//
// It is also how an instance recovers from a trap, so unlike every other
// operation it is allowed on a dead one -- the replacement is a new module with
// its own memory, and nothing carries over.
func (i *Instance) Reset(ctx context.Context) error {
	if err := i.acquire(ctx); err != nil {
		return err
	}
	defer i.release()

	old := i.abi.Load()

	var trap *host.TrapError
	if err := old.Err(); err != nil && !errors.As(err, &trap) {
		return err
	}

	next, err := host.New()
	if err != nil {
		return err
	}

	i.abi.Store(next)

	old.Close()
	old.Release()
	return nil
}

// Restore returns this instance to the snapshot, discarding every global and
// definition made since, and invalidating any Func resolved before it.
func (i *Instance) Restore(ctx context.Context, s *host.Snapshot) error {
	if err := i.acquire(ctx); err != nil {
		return err
	}
	defer i.release()

	old := i.abi.Load()

	err := old.Err()

	var trap *host.TrapError
	if err != nil && !errors.As(err, &trap) {
		// Closed, not trapped. A closed instance stays closed.
		return err
	}

	if err == nil {
		if err := old.RestoreInto(s); err != nil {
			return err
		}
		return nil
	}

	// Trapped: the module cannot be trusted to hold the copy, so it is
	// replaced rather than written over.
	next, err := s.Restore()
	if err != nil {
		return err
	}

	i.abi.Store(next)

	old.Close()
	old.Release()
	return nil
}

// ------------------------------------------------------------------------

// acquire takes the instance, giving up if ctx ends first..
func (i *Instance) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case i.lock <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *Instance) release() {
	<-i.lock
}

// run holds the instance for the duration of fn, with the guest interrupted if
// ctx ends first.
func (i *Instance) run(ctx context.Context, fn func(*host.ABI) error) error {
	if err := i.acquire(ctx); err != nil {
		return err
	}
	defer i.release()

	abi := i.abi.Load()
	if err := abi.Begin(); err != nil {
		return err
	}

	if ctx.Done() != nil {
		stop := context.AfterFunc(ctx, abi.Cancel)
		defer stop()
	}

	err := fn(abi)

	// A trap outranks the deadline: the instance is dead, and reporting only
	// DeadlineExceeded would invite the caller to retry it forever.
	var trap *host.TrapError
	if ctxErr := ctx.Err(); ctxErr != nil && !errors.As(err, &trap) {
		return ctxErr
	}
	return err
}

// FromSnapshot builds a new interpreter from a snapshot. It is independent of
// the instance the snapshot came from and of every other one restored from it,
// which is what makes a snapshot the way to fan out across goroutines.
func FromSnapshot(s *host.Snapshot) (*Instance, error) {
	abi, err := s.Restore()
	if err != nil {
		return nil, err
	}

	i := &Instance{lock: make(chan struct{}, 1)}
	i.abi.Store(abi)
	return i, nil
}
