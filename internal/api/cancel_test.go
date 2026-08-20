package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The whole point: a guest loop must not be able to wedge the host.
func TestCancelInterruptsLoop(t *testing.T) {
	in, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		in.Cancel()
	}()

	start := time.Now()
	done := make(chan error, 1)
	go func() { _, err := in.Exec("while True:\n    pass\n"); done <- err }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("infinite loop returned without an error")
		}
		t.Logf("interrupted after %v: %v", time.Since(start).Round(time.Millisecond), err)
	case <-time.After(10 * time.Second):
		t.Fatal("guest loop was not interrupted")
	}

	// The interpreter has to still work afterwards.
	if got, err := in.Eval("1 + 1"); err != nil || got != int64(2) {
		t.Errorf("after cancel: %#v, %v", got, err)
	}
}

func TestCloseInterruptsLoop(t *testing.T) {
	in, err := New()
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		in.Close()
	}()

	done := make(chan struct{})
	go func() { defer close(done); in.Exec("while True:\n    pass\n") }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not interrupt the guest")
	}
}

func TestWithContextDeadline(t *testing.T) {
	in, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = in.WithContext(ctx, func() error {
		_, execErr := in.Exec("while True:\n    pass\n")
		return execErr
	})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want DeadlineExceeded", err)
	}
	t.Logf("deadline honoured after %v", time.Since(start).Round(time.Millisecond))

	if got, err := in.Eval("1 + 1"); err != nil || got != int64(2) {
		t.Errorf("after deadline: %#v, %v", got, err)
	}
}

// A guest that catches everything must not be able to swallow cancellation
// forever.
func TestCancelSurvivesGuestExceptHandler(t *testing.T) {
	in, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		in.Cancel()
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		in.Exec("while True:\n    try:\n        pass\n    except:\n        pass\n")
	}()

	select {
	case <-done:
		t.Log("returned even though the guest swallows exceptions")
	case <-time.After(10 * time.Second):
		t.Fatal("a bare except: in the guest defeated cancellation")
	}
}
