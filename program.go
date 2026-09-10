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
// state changes. Call [Program.Close] when done.
type Program struct {
	snap *api.Snapshot

	maxIdle int

	mu     sync.Mutex
	free   []*Instance
	closed bool
}

// BorrowedInstance provides interpreter operations during [Program.Run].
// It must not be copied or used after the callback returns. All goroutines
// using it must finish before the callback returns.
type BorrowedInstance struct {
	wrapped *Instance
	mu      sync.Mutex
	closed  bool
}

// ErrRunReturned is reported by a [BorrowedInstance] used after its
// [Program.Run] callback returned.
var ErrRunReturned = errors.New("micropython: BorrowedInstance used after Run returned")

// Call invokes a Python global function; see [Instance.Call].
func (o *BorrowedInstance) Call(ctx context.Context, name string, args ...any) (Value, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return Value{}, ErrRunReturned
	}

	return o.wrapped.Call(ctx, name, args...)
}

// Eval evaluates a Python expression; see [Instance.Eval].
func (o *BorrowedInstance) Eval(ctx context.Context, expr string) (Value, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return Value{}, ErrRunReturned
	}
	return o.wrapped.Eval(ctx, expr)
}

// Get reads a Python global; see [Instance.Get].
func (o *BorrowedInstance) Get(ctx context.Context, name string) (Value, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return Value{}, ErrRunReturned
	}

	return o.wrapped.Get(ctx, name)
}

// Set binds a Python global; see [Instance.Set].
func (o *BorrowedInstance) Set(ctx context.Context, name string, v any) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return ErrRunReturned
	}

	return o.wrapped.Set(ctx, name, v)
}

// Exec runs Python statements; see [Instance.Exec].
func (o *BorrowedInstance) Exec(ctx context.Context, src string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return ErrRunReturned
	}

	return o.wrapped.Exec(ctx, src)
}

// NewProgram initializes Python using opts and saves the state for later runs.
// Initialization must close files, directory iterators, and sockets before
// the state is saved. The caller must close the Program when done.
func NewProgram(ctx context.Context, opts ...ProgramOption) (*Program, error) {
	// TODO(gregfurman): Consider catering for warm and cold starts
	opt := newOptions(opts)
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

// Instance creates a standalone interpreter from the Program's initialized state.
// It keeps state between calls. The caller must close it separately.
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

// Run calls fn with a borrowed interpreter, then resets or closes it.
// It returns callback and cleanup errors. External effects are not undone.
//
// Canceling ctx requests interruption of the current Python operation; see
// [Instance.Cancel]. Pass ctx to borrowed methods so later operations also
// observe cancellation. The callback must handle cancellation of its own Go work.
// The [BorrowedInstance] is valid only until fn returns.
func (p *Program) Run(ctx context.Context, fn func(in *BorrowedInstance) error) (err error) {
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

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stop := context.AfterFunc(ctx, func() {
		in.Cancel()
	})

	defer stop()

	lent := &BorrowedInstance{wrapped: in}
	defer func() {
		lent.mu.Lock()
		lent.closed = true
		lent.mu.Unlock()
	}()

	return fn(lent)
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
