package micropython

import (
	"context"
	"maps"
	"runtime"
	"slices"
	"sync"

	"github.com/gregfurman/micropython-go/internal/api"
	"github.com/gregfurman/micropython-go/internal/host"
)

// Program represents a pre-compiled MicroPython script that is safe for
// concurrent use.
//
// When a Program is compiled, it executes the source code once and captures a
// snapshot of the resulting interpreter state. This snapshot serves as a pristine
// baseline for all future function calls.
//
// To prevent memory leaks, Close must be called when the Program is no longer needed.
type Program struct {
	snap *host.Snapshot

	maxIdle int

	mu     sync.Mutex
	free   []*Instance
	closed bool
}

// Compile evaluates the provided Python source code and captures its initial state.
//
// The resulting Program manages an idle pool of interpreters sized by WithPoolSize
// (which defaults to the number of logical CPUs via runtime.NumCPU).
func Compile(ctx context.Context, src string, opts ...option) (*Program, error) {
	// TODO(gregfurman): Consider catering for warm and cold starts
	opt := newOptions(opts)
	if opt.programPoolSize == 0 {
		opt.programPoolSize = max(runtime.NumCPU(), 1)
	}

	in, err := api.New()
	if err != nil {
		return nil, err
	}

	for _, key := range slices.Sorted(maps.Keys(opt.globals)) {
		if err := in.Set(ctx, key, opt.globals[key].val); err != nil {
			in.Close()
			return nil, err
		}
	}

	if _, err := in.Exec(ctx, src); err != nil {
		in.Close()
		return nil, err
	}

	snap, err := in.Snapshot(ctx)
	if err != nil {
		in.Close()
		return nil, err
	}

	return &Program{
		snap:    snap,
		maxIdle: opt.programPoolSize,
		free:    []*Instance{{in: in}},
	}, nil
}

// Instance spawns a standalone Python interpreter initialized with the compiled
// source's state.
//
// Unlike Call, an Instance is stateful: variable mutations and definitions will
// persist across evaluations. The returned Instance is completely detached from
// the Program's pool and must be closed by the caller.
func (p *Program) Instance(ctx context.Context) (*Instance, error) {
	return fromSnapshot(p.snap)
}

// Call invokes a named Python function with the provided arguments.
//
// The function executes in total isolation. Any changes made to Python's global
// state during the execution are discarded before the underlying interpreter is
// returned to the internal pool.
func (p *Program) Call(ctx context.Context, name string, args ...any) (any, error) {
	in, err := p.acquire()
	if err != nil {
		return nil, err
	}

	defer p.release(in)

	return in.Call(ctx, name, args...)
}

// Close releases every interpreter the Program is holding. Calls after it
// return ErrClosed.
func (p *Program) Close() error {
	p.mu.Lock()
	free := p.free
	p.free, p.closed = nil, true
	p.mu.Unlock()

	for _, in := range free {
		in.Close()
	}
	return nil
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
		in.Close()
	}
}

// release rewinds the interpreter to the compiled source and puts it back, so
// the pool holds nothing a call left behind. Beyond maxIdle it is closed
// instead, which is what bounds the pool.
func (p *Program) release(in *Instance) {
	if err := in.restore(p.snap); err != nil {
		in.Close()
		return
	}

	p.mu.Lock()
	keep := !p.closed && len(p.free) < p.maxIdle
	if keep {
		p.free = append(p.free, in)
	}
	p.mu.Unlock()

	if !keep {
		in.Close()
	}
}
