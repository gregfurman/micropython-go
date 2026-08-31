package api

import (
	"context"
	"errors"
	"io"
	"runtime/debug"
	"sync/atomic"

	"github.com/gregfurman/micropython-go/internal/host"
	"github.com/gregfurman/micropython-go/internal/value"
)

// Instance serialises access to one minimal MicroPython runtime.
type Instance struct {
	lock chan struct{}
	rt   atomic.Pointer[host.Module]

	heapBytes int32
	stdout    io.Writer
	closed    atomic.Bool
	trap      atomic.Pointer[TrapError]
}

func New(heapBytes int32, stdout io.Writer) (*Instance, error) {
	if heapBytes < 0 {
		return nil, errors.New("micropython: heap size cannot be negative")
	}
	rt, err := host.NewModule(uint(heapBytes), stdout)
	if err != nil {
		return nil, err
	}
	i := &Instance{lock: make(chan struct{}, 1), heapBytes: heapBytes, stdout: stdout}
	i.rt.Store(rt)
	return i, nil
}

func (i *Instance) Exec(ctx context.Context, src string) error {
	return i.run(ctx, func(rt *host.Module) error {
		return rt.Exec(src)
	})
}

func (i *Instance) Eval(ctx context.Context, expr string) (out any, err error) {
	err = i.run(ctx, func(rt *host.Module) error {
		out, err = rt.Eval(expr)
		return err
	})
	return out, err
}

func (i *Instance) Call(ctx context.Context, name string, args ...any) (out any, err error) {
	err = i.run(ctx, func(rt *host.Module) error {
		out, err = rt.Call(name, args)
		return err
	})
	return out, err
}

func (i *Instance) Set(ctx context.Context, name string, v value.Value) error {
	return i.run(ctx, func(rt *host.Module) error { return rt.Set(name, v) })
}

func (i *Instance) DefineFunction(ctx context.Context, name string, fn host.HostFunc) error {
	return i.run(ctx, func(rt *host.Module) error { return rt.DefineFunction(name, fn) })
}

func (i *Instance) Cancel() {
	if rt := i.rt.Load(); rt != nil {
		rt.Cancel()
	}
}

func (i *Instance) Err() error {
	if t := i.trap.Load(); t != nil {
		return t
	}
	if i.closed.Load() {
		return ErrClosed
	}
	return nil
}

func (i *Instance) Close() error {
	i.Cancel()
	i.lock <- struct{}{}
	defer i.release()
	i.closed.Store(true)
	i.rt.Store(nil)
	return nil
}

func (i *Instance) Snapshot(ctx context.Context) (*host.Snapshot, error) {
	if err := i.acquire(ctx); err != nil {
		return nil, err
	}
	defer i.release()
	if err := i.Err(); err != nil {
		return nil, err
	}
	return i.rt.Load().Snapshot(), nil
}

func (i *Instance) Reset(ctx context.Context) error {
	if err := i.acquire(ctx); err != nil {
		return err
	}
	defer i.release()
	if i.closed.Load() {
		return ErrClosed
	}
	next, err := host.NewModule(uint(i.heapBytes), i.stdout)
	if err != nil {
		return err
	}
	i.rt.Store(next)
	i.trap.Store(nil)
	return nil
}

func (i *Instance) Restore(ctx context.Context, s *host.Snapshot) error {
	if err := i.acquire(ctx); err != nil {
		return err
	}
	defer i.release()
	if i.closed.Load() {
		return ErrClosed
	}
	if i.trap.Load() != nil {
		next, err := s.Restore()
		if err != nil {
			return err
		}
		i.rt.Store(next)
		i.trap.Store(nil)
		return nil
	}
	return i.rt.Load().Restore(s)
}

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

func (i *Instance) release() { <-i.lock }

func (i *Instance) run(ctx context.Context, fn func(*host.Module) error) (err error) {
	if err := i.acquire(ctx); err != nil {
		return err
	}
	defer i.release()
	if err := i.Err(); err != nil {
		return err
	}

	rt := i.rt.Load()
	rt.Begin()
	if ctx.Done() != nil {
		stop := context.AfterFunc(ctx, rt.Cancel)
		defer stop()
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			t := &TrapError{Value: recovered, Stack: debug.Stack()}
			i.trap.CompareAndSwap(nil, t)
			err = t
		}
	}()

	err = fn(rt)
	var trap *TrapError
	if ctxErr := ctx.Err(); ctxErr != nil && !errors.As(err, &trap) {
		return ctxErr
	}
	return err
}

func FromSnapshot(s *host.Snapshot) (*Instance, error) {
	rt, err := s.Restore()
	if err != nil {
		return nil, err
	}
	i := &Instance{lock: make(chan struct{}, 1), stdout: s.Stdout()}
	i.rt.Store(rt)
	return i, nil
}

type Snapshot = host.Snapshot
