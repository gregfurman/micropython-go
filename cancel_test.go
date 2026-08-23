package micropython

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Cancellation is the only way to stop a guest. There is no scheduler, no
// signal and no second thread inside the module, so a `while True:` runs until
// the VM hook asks the host whether to stop -- and if that hook is ever lost,
// a single call wedges the process. These tests are what would notice.

const spinSrc = `
def spin():
    while True:
        pass

def spin_swallowing():
    while True:
        try:
            pass
        except:
            pass

def double(n):
    return n * 2
`

func spinner(t *testing.T) *Program {
	t.Helper()
	p, err := Compile(context.Background(), spinSrc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

// A deadline stops a runaway guest, and the Program is usable afterwards.
func TestCancelDeadline(t *testing.T) {
	p := spinner(t)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := p.Call(ctx, "spin")
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("spin() = %v, want DeadlineExceeded", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("took %v to stop, want promptly after the deadline", elapsed)
	}

	if got, err := p.Call(context.Background(), "double", Of(int64(21))); err != nil || got != int64(42) {
		t.Errorf("after cancellation: %#v, %v", got, err)
	}
}

// A context already dead on arrival must not run the guest at all.
func TestCancelBeforeCall(t *testing.T) {
	p := spinner(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.Call(ctx, "spin"); !errors.Is(err, context.Canceled) {
		t.Fatalf("spin() = %v, want Canceled", err)
	}
	if got, err := p.Call(context.Background(), "double", Of(int64(1))); err != nil || got != int64(2) {
		t.Errorf("after a pre-cancelled call: %#v, %v", got, err)
	}
}

// Cancellation is a level, not an edge. A guest with a bare `except:` catches
// the KeyboardInterrupt the hook raises, so a one-shot request would be
// swallowed and the loop would run forever; the request has to stay set for
// the rest of the call.
func TestCancelIsNotSwallowedByBareExcept(t *testing.T) {
	p := spinner(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := p.Call(ctx, "spin_swallowing")
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("spin_swallowing() = %v, want DeadlineExceeded", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a guest catching KeyboardInterrupt outran cancellation")
	}
}

// Cancelling one call must not disturb another that is already running. The
// cancellation request is per-interpreter, and the pool hands each concurrent
// call its own, so a short deadline on one must not cut short a long one.
func TestCancelIsPerCall(t *testing.T) {
	p, err := Compile(context.Background(), `
def work(n):
    total = 0
    for i in range(n):
        total += i
    return total

def spin():
    while True:
        pass
`)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	long := make(chan error, 1)
	go func() {
		_, err := p.Call(context.Background(), "work", Of(int64(2_000_000)))
		long <- err
	}()

	// Give the long call time to be in flight, then cancel a different one.
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := p.Call(ctx, "spin"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("spin() = %v, want DeadlineExceeded", err)
	}

	select {
	case err := <-long:
		if err != nil {
			t.Errorf("the long call was cut short by another call's deadline: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the long call never finished")
	}
}

// Instance.Cancel stops a call from another goroutine, with no context
// involved.
func TestInstanceCancel(t *testing.T) {
	in := newT(t)
	if _, err := in.Exec(t.Context(), spinSrc); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := in.Call(context.Background(), "spin")
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if err := in.Cancel(); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("spin() returned without an error after Cancel")
		}
		if !errors.Is(err, ErrInterrupted) {
			t.Logf("cancelled call reported: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Cancel did not stop the guest")
	}

	// Cancel is a request for one call, not a permanent state.
	if got, err := in.Call(context.Background(), "double", Of(int64(4))); err != nil || got != int64(8) {
		t.Errorf("after Cancel: %#v, %v", got, err)
	}
}
