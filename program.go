package micropython

import (
	"context"
	"sync"

	"github.com/gregfurman/micropython-wasi/internal/api"
	"github.com/gregfurman/micropython-wasi/internal/host"
)

// Program
type Program struct {
	snap *host.Snapshot

	mu     sync.Mutex
	free   []*Instance
	closed bool
}

func Compile(ctx context.Context, src string) (*Program, error) {
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

	return &Program{snap: snap, free: []*Instance{{in: in}}}, nil
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

func (p *Program) acquire() (*Instance, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrClosed
	}
	if n := len(p.free); n > 0 {
		in := p.free[n-1]
		p.free = p.free[:n-1]
		p.mu.Unlock()
		return in, nil
	}
	p.mu.Unlock()

	return p.Instance(context.TODO())
}

// release rewinds the interpreter and returns it to the pool.
func (p *Program) release(in *Instance) {
	if err := in.restore(p.snap); err != nil {
		in.Close()
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		in.Close()
		return
	}
	p.free = append(p.free, in)
}
