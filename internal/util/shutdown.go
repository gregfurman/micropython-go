package util

import (
	"context"
	"sync"
)

type Signaller struct {
	ch       chan struct{}
	stopOnce sync.Once
	mu       sync.RWMutex
}

func NewSignaller() *Signaller {
	return &Signaller{
		ch: make(chan struct{}, 1),
	}
}

func (s *Signaller) Trigger() {
	s.stopOnce.Do(func() {
		close(s.ch)
	})
}

// func (s *Signaller) Reset() {
// 	s.mu.Lock()
// 	defer s.mu.Unlock()

// 	s.stopOnce = sync.Once{}
// 	s.ch = make(chan struct{}, 1)
// }

// func (s *Signaller) TriggerInterrupt() {
// 	select {
// 	case s.ch <- struct{}{}:
// 	default:
// 	}
// }

func (s *Signaller) StopChan() <-chan struct{} {
	return s.ch
}

func (s *Signaller) Context(ctx context.Context) (context.Context, context.CancelFunc) {
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(ctx)
	go func() {
		select {
		case <-ctx.Done():
		case <-s.ch:
		}
		cancel()
	}()
	return ctx, cancel
}
