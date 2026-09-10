package api

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"runtime/debug"
	"sync/atomic"

	"github.com/gregfurman/micropython-go/internal/host"
	"github.com/gregfurman/micropython-go/internal/host/network"
	"github.com/gregfurman/micropython-go/internal/value"
)

// Instance serialises access to one minimal MicroPython runtime.
type Instance struct {
	lock chan struct{}
	rt   atomic.Pointer[host.Module]

	heapBytes  int32
	stdout     io.Writer
	network    network.Config
	filesystem fs.FS
	vars       map[string]string
	closed     atomic.Bool
	trap       atomic.Pointer[TrapError]
}

func New(heapBytes int32, stdout io.Writer, config network.Config, filesystem fs.FS, vars map[string]string) (*Instance, error) {
	if heapBytes < 0 {
		return nil, errors.New("micropython: heap size cannot be negative")
	}
	rt, err := host.NewModule(uint(heapBytes), stdout, config, filesystem, vars)
	if err != nil {
		return nil, err
	}
	i := &Instance{
		lock:       make(chan struct{}, 1),
		heapBytes:  heapBytes,
		stdout:     stdout,
		network:    config,
		filesystem: filesystem,
		vars:       vars,
	}
	i.rt.Store(rt)
	return i, nil
}

func (i *Instance) Exec(ctx context.Context, src string) error {
	return i.run(ctx, func(rt *host.Module) error {
		return rt.Exec(src)
	})
}

func (i *Instance) Eval(ctx context.Context, expr string) (out value.Value, err error) {
	err = i.run(ctx, func(rt *host.Module) error {
		out, err = rt.Eval(expr)
		return err
	})
	return out, err
}

func (i *Instance) Call(ctx context.Context, name string, args ...any) (out value.Value, err error) {
	err = i.run(ctx, func(rt *host.Module) error {
		out, err = rt.Call(name, args)
		return err
	})
	return out, err
}

func (i *Instance) Set(ctx context.Context, name string, v any) error {
	return i.run(ctx, func(rt *host.Module) error { return rt.Set(name, v) })
}

func (i *Instance) CallRef(ctx context.Context, obj value.Object, args []any) (out value.Value, err error) {
	err = i.run(ctx, func(rt *host.Module) error {
		out, err = rt.CallRef(obj, args)
		return err
	})
	return out, err
}

func (i *Instance) Resolve(ctx context.Context, obj value.Object) (out value.Value, err error) {
	err = i.run(ctx, func(rt *host.Module) error {
		out, err = rt.Resolve(obj)
		return err
	})
	return out, err
}

func (i *Instance) Release(ctx context.Context, refs ...*value.Ref) error {
	return i.run(ctx, func(rt *host.Module) error {
		rt.Release(refs...)
		return nil
	})
}

// NextGenerator advances a guest generator while holding the instance lock.
func (i *Instance) NextGenerator(ctx context.Context, obj value.Object) (out value.Value, more bool, err error) {
	err = i.run(ctx, func(rt *host.Module) error {
		out, more, err = rt.NextGenerator(obj)
		return err
	})
	return out, more, err
}

func (i *Instance) Get(ctx context.Context, name string) (out value.Value, err error) {
	err = i.run(ctx, func(rt *host.Module) error {
		out, err = rt.Get(name)
		return err
	})
	return out, err
}

func (i *Instance) DefineFunction(ctx context.Context, name string, fn host.HostFunc) error {
	return i.run(ctx, func(rt *host.Module) error {
		return rt.DefineFunction(name, fn)
	})
}

func (i *Instance) Cancel() {
	if rt := i.rt.Load(); rt != nil {
		rt.Cancel()
	}
}

func (i *Instance) CancelWithReason(err error) {
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
	if rt := i.rt.Swap(nil); rt != nil {
		return rt.Close()
	}
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
	rt := i.rt.Load()
	rt.ReleasePendingRefs() // exclude references whose cleanups have run
	return rt.Snapshot()
}

func (i *Instance) Reset(ctx context.Context) error {
	if err := i.acquire(ctx); err != nil {
		return err
	}
	defer i.release()
	if i.closed.Load() {
		return ErrClosed
	}
	next, err := host.NewModule(uint(i.heapBytes), i.stdout, i.network, i.filesystem, i.vars)
	if err != nil {
		return err
	}
	previous := i.rt.Swap(next)
	i.trap.Store(nil)
	return previous.Close()
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
		previous := i.rt.Swap(next)
		i.network = s.NetworkConfig()
		i.filesystem = s.Filesystem()
		i.vars = s.Vars()
		i.trap.Store(nil)
		return previous.Close()
	}
	if err := i.rt.Load().Restore(s); err != nil {
		return err
	}
	i.network = s.NetworkConfig()
	i.filesystem = s.Filesystem()
	i.vars = s.Vars()
	return nil
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

func (i *Instance) Context(ctx context.Context) (context.Context, context.CancelFunc) {
	return i.rt.Load().Context(ctx)
}

func (i *Instance) run(ctx context.Context, fn func(*host.Module) error) (err error) {
	if err := i.acquire(ctx); err != nil {
		return err
	}
	defer i.release()
	if err := i.Err(); err != nil {
		return err
	}

	rt := i.rt.Load()
	defer func() {
		if recovered := recover(); recovered != nil {
			t := &TrapError{Value: recovered, Stack: debug.Stack()}
			i.trap.CompareAndSwap(nil, t)
			err = t
		}
	}()

	rt.Begin()
	opCtx, cancel := rt.Context(ctx)
	defer cancel()

	rt.SetNetworkContext(opCtx)
	defer rt.SetNetworkContext(nil)

	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		rt.Cancel()
		close(done)
	})
	defer func() {
		if !stop() {
			<-done
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
	i := &Instance{lock: make(chan struct{}, 1), stdout: s.Stdout(), network: s.NetworkConfig(), filesystem: s.Filesystem(), vars: s.Vars()}
	i.rt.Store(rt)
	return i, nil
}

type Snapshot = host.Snapshot
