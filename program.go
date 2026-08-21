package micropython

import (
	"context"
	"runtime"
	"sync"

	"github.com/gregfurman/micropython-wasi/internal/api"
	"github.com/gregfurman/micropython-wasi/internal/host"
)

// Program
type Program struct {
	snap *host.Snapshot

	maxIdle int

	mu     sync.Mutex
	free   []*Instance
	closed bool
}

func Compile(ctx context.Context, src string, opts ...option) (*Program, error) {
	// TODO(gregfurman): Consider catering for warm and cold starts
	opt := &options{
		programPoolSize: max(runtime.NumCPU(), 1),
	}
	for _, o := range opts {
		o(opt)
	}

	in, err := api.New()
	if err != nil {
		return nil, err
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

func (p *Program) Instance(ctx context.Context) (*Instance, error) {
	// NOTE: this is not from the pool
	return fromSnapshot(p.snap)
}

func (p *Program) Call(ctx context.Context, name string, args ...any) (any, error) {
	in, err := p.acquire()
	if err != nil {
		return nil, err
	}

	defer p.release(in)

	return in.Call(ctx, name, args...)
}

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

// release rewinds the interpreter and returns it to the pool.
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
