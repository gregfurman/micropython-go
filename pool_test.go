package micropython

import (
	"context"
	"sync"
	"testing"
)

// A burst of concurrent calls makes as many interpreters as it needs, but the
// pool must not stay that wide once the burst is over.
func TestPoolBounded(t *testing.T) {
	p, err := Compile(context.Background(), "def f(n):\n    return n\n", WithPoolSize(12))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	max := p.maxIdle

	const burst = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range burst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // all in flight at once, forcing the pool to grow
			if _, err := p.Call(t.Context(), "f", Of(int64(i))); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()

	p.mu.Lock()
	idle, capacity := len(p.free), cap(p.free)
	p.mu.Unlock()

	t.Logf("after a burst of %d: idle=%d cap=%d maxIdle=%d", burst, idle, capacity, max)
	if idle > max {
		t.Errorf("pool kept %d idle interpreters, want at most %d", idle, max)
	}

	if got, err := p.Call(t.Context(), "f", Of(int64(7))); err != nil || got != int64(7) {
		t.Errorf("after burst: %#v, %v", got, err)
	}
}
