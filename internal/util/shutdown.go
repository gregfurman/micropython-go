package util

import (
	"context"
	"errors"
	"sync"
)

var ErrShutdownTriggered = errors.New("shutdown signal was triggered")

type Signaller struct {
	mu sync.RWMutex
	ch chan struct{}
}

func NewSignaller() *Signaller {
	return &Signaller{
		ch: make(chan struct{}, 1),
	}
}

func (s *Signaller) Trigger() {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-s.ch:
	default:
		close(s.ch)
	}
}

func (s *Signaller) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	select {
	case <-s.ch:
		s.ch = make(chan struct{}, 1)
	default:
	}
}

func (s *Signaller) StopChan() <-chan struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.ch
}

func (s *Signaller) Context(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	stop := s.StopChan()

	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		cancel(ErrShutdownTriggered)
	}()

	return ctx, func() {
		cancel(ErrShutdownTriggered)
	}
}
