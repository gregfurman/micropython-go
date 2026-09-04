package micropython

import (
	"context"
	"runtime"
	"slices"
	"sync"

	"github.com/gregfurman/micropython-go/internal/api"
)

// Program is a pool of pre-compiled MicroPython interpreters, safe for
// concurrent use.
//
// Compile builds one interpreter, configures it with the given options, and
// snapshots it. That snapshot is the baseline every later call starts from: a
// call borrows an interpreter, runs, and the interpreter is rewound to the
// snapshot before returning to the pool, so calls cannot see each other's
// changes to Python state.
//
// Close must be called when the Program is no longer needed.
type Program struct {
	snap *api.Snapshot

	maxIdle int

	mu     sync.Mutex
	free   []*Instance
	closed bool
}

// Compile builds an interpreter from opts and captures it as the Program's
// starting state. Use CompileSource to run Python source as part of that state.
//
// WithPoolSize sets how many interpreters stay idle between calls, defaulting
// to runtime.NumCPU; it is not a ceiling on how many exist at once.
func Compile(ctx context.Context, opts ...ProgramOption) (*Program, error) {
	// TODO(gregfurman): Consider catering for warm and cold starts
	opt := newOptions(opts)
	if opt.programPoolSize == 0 {
		opt.programPoolSize = max(runtime.NumCPU(), 1)
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
		maxIdle: opt.programPoolSize,
		free:    []*Instance{in},
	}, nil
}

// CompileSource runs src at module level and captures the result as the
// Program's starting state. Shorthand for Compile with WithSourceScript.
func CompileSource(ctx context.Context, src string, opts ...ProgramOption) (*Program, error) {
	return Compile(ctx, append(slices.Clip(opts), WithSourceScript(src))...)
}

// Instance spawns a standalone Python interpreter initialized with the compiled
// source's state.
//
// Unlike a Program, an Instance is stateful: variable mutations and definitions will
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
func (p *Program) Call(ctx context.Context, name string, args ...any) (Value, error) {
	in, err := p.acquire()
	if err != nil {
		return Value{}, err
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

// release puts the interpreter back, rewound to the compiled source so the
// pool holds nothing a call left behind. Beyond maxIdle it is closed instead,
// which is what bounds the pool.
//
// The decision comes before the rewind: rewinding costs a copy of the whole
// interpreter, and there is no point paying it for one about to be closed.
func (p *Program) release(in *Instance) {
	p.mu.Lock()
	keep := !p.closed && len(p.free) < p.maxIdle
	p.mu.Unlock()

	if !keep {
		in.Close()
		return
	}

	if err := in.restore(p.snap); err != nil {
		in.Close()
		return
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
		in.Close()
	}
}
