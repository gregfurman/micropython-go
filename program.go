package micropython

import (
	"context"
	"errors"
	"runtime"
	"sync"

	"github.com/gregfurman/micropython-go/internal/api"
)

// Program pools interpreters initialized from a common Python state.
// Runs are safe to execute concurrently and do not retain each other's Python
// state changes. Call Close when done.
type Program struct {
	snap *api.Snapshot

	maxIdle int

	mu     sync.Mutex
	free   []*Instance
	closed bool
}

// OwnedInstance provides interpreter operations during [Program.Run].
// It must not be retained or used after the callback returns.
type OwnedInstance struct {
	wrapped *Instance
}

// Call invokes a Python global function; see [Instance.Call].
func (o *OwnedInstance) Call(ctx context.Context, name string, args ...any) (Value, error) {
	return o.wrapped.Call(ctx, name, args...)
}

// Eval evaluates a Python expression; see [Instance.Eval].
func (o *OwnedInstance) Eval(ctx context.Context, expr string) (Value, error) {
	return o.wrapped.Eval(ctx, expr)
}

// Get reads a Python global; see [Instance.Get].
func (o *OwnedInstance) Get(ctx context.Context, name string) (Value, error) {
	return o.wrapped.Get(ctx, name)
}

// Set binds a Python global; see [Instance.Set].
func (o *OwnedInstance) Set(ctx context.Context, name string, v any) error {
	return o.wrapped.Set(ctx, name, v)
}

// Exec runs Python statements; see [Instance.Exec].
func (o *OwnedInstance) Exec(ctx context.Context, src string) error {
	return o.wrapped.Exec(ctx, src)
}

// Compile applies opts, runs src, and snapshots the resulting state for a Program.
// A nonempty src overrides [WithSource]; an empty src uses it if provided.
// The caller must close the Program when done.
// Initialization must close files, directory iterators, and sockets before the
// state is snapshotted. Filesystem changes are not included in the snapshot.
func Compile(ctx context.Context, src string, opts ...ProgramOption) (*Program, error) {
	// TODO(gregfurman): Consider catering for warm and cold starts
	opt := newOptions(opts)
	if src != "" {
		opt.sourceScript = src
	}
	if err := opt.validate(); err != nil {
		return nil, err
	}
	if opt.maxIdle == 0 {
		opt.maxIdle = max(runtime.NumCPU(), 1)
	}

	in, err := newInstance(ctx, opt)
	if err != nil {
		return nil, err
	}

	snap, err := in.wrapped.Snapshot(ctx)
	if err != nil {
		in.Close()
		return nil, err
	}

	return &Program{
		snap:    snap,
		maxIdle: opt.maxIdle,
		free:    []*Instance{in},
	}, nil
}

// Instance creates a standalone interpreter from the compiled state.
// It is not pooled: state persists across calls, and the caller must close it.
func (p *Program) Instance(ctx context.Context) (*Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}

	return fromSnapshot(p.snap)
}

// Run lends an interpreter to fn, then rewinds or discards it, even on error
// or panic. Cleanup failures are joined with fn's error; otherwise it is
// returned unchanged. Panics propagate after cleanup.
// External effects, including changes made by Go callbacks, are not rewound.
// Open files and sockets are closed when the interpreter is returned.
//
// Use the callback's context: it inherits ctx and is canceled when fn exits.
// Cancellation is cooperative; Run waits for fn but not its goroutines.
// Finish all interpreter-using work before returning. Do not retain the
// OwnedInstance or use guest handles afterward, including handles nested in
// exported containers. Only detached Go data may outlive the run.
func (p *Program) Run(ctx context.Context, fn func(ctx context.Context, in *OwnedInstance) error) (err error) {
	if fn == nil {
		return errors.New("micropython: Run needs a function to run")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	in, err := p.acquire()
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := p.release(in); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()

	if err := ctx.Err(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	return fn(ctx, &OwnedInstance{wrapped: in})
}

// Close closes idle interpreters and rejects new work with [ErrClosed].
// Active runs finish normally; their interpreters are closed on return.
func (p *Program) Close() error {
	p.mu.Lock()
	free := p.free
	p.free, p.closed = nil, true
	p.mu.Unlock()

	var err error
	for _, in := range free {
		err = errors.Join(err, in.Close())
	}
	return err
}

// acquire takes an interpreter from the pool, or makes one if none is free.
func (p *Program) acquire() (*Instance, error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, ErrClosed
		}

		n := len(p.free)
		if n == 0 {
			p.mu.Unlock()
			return fromSnapshot(p.snap)
		}

		in := p.free[n-1]
		p.free[n-1] = nil
		p.free = p.free[:n-1]
		p.mu.Unlock()

		if in.Err() == nil {
			return in, nil
		}
		if err := in.Close(); err != nil {
			return nil, err
		}
	}
}

// release puts the interpreter back, rewound to the compiled source so the
// pool holds nothing a call left behind. Beyond maxIdle it is closed instead,
// which is what bounds the pool.
//
// The decision comes before the rewind: rewinding costs a copy of the whole
// interpreter, and there is no point paying it for one about to be closed.
func (p *Program) release(in *Instance) error {
	p.mu.Lock()
	keep := !p.closed && len(p.free) < p.maxIdle
	p.mu.Unlock()

	if !keep {
		return in.Close()
	}

	if err := in.restore(p.snap); err != nil {
		return errors.Join(err, in.Close())
	}

	p.mu.Lock()
	// Checked again: the Program may have closed, or another goroutine filled
	// the last slot, while the rewind was running.
	keep = !p.closed && len(p.free) < p.maxIdle
	if keep {
		p.free = append(p.free, in)
	}
	p.mu.Unlock()

	if !keep {
		return in.Close()
	}
	return nil
}
