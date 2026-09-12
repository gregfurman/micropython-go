package micropython

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// These measure the reference-release limitation recorded in ARCHITECTURE.md.
// A failure here means the numbers in "Known limitations" need revisiting.
//
// The controls matter as much as the measurement: without them it is easy to
// blame the host for what is really the cost of the objects, or for what plain
// MicroPython does on its own.

const memSlack = 4096

// guestFree collects the guest heap and reports what is free. Calling it is
// also what applies any queued reference releases, since every operation
// starts with Begin.
func guestFree(t *testing.T, in *Instance) int64 {
	t.Helper()

	ctx := context.Background()
	if err := in.Exec(ctx, "gc.collect()"); err != nil {
		t.Fatal(err)
	}

	v, err := in.Eval(ctx, "gc.mem_free()")
	if err != nil {
		t.Fatal(err)
	}

	n, err := v.AsInt()
	if err != nil {
		t.Fatal(err)
	}

	return n
}

// waitForCleanups forces a collection and waits for the cleanup queue to
// drain. AddCleanup callbacks run asynchronously after the collection that
// found them, so a bare runtime.GC can return before any ref was queued.
func waitForCleanups(t *testing.T) {
	t.Helper()

	for range 3 {
		done := make(chan struct{})

		func() {
			sentinel := new([64]byte)
			runtime.AddCleanup(sentinel, func(c chan struct{}) { close(c) }, done)
		}()
		runtime.GC()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("cleanup queue did not drain")
		}
	}
}

func newMemProbe(t *testing.T) *Instance {
	t.Helper()

	in := newT(t)
	if err := in.Exec(context.Background(), "import gc"); err != nil {
		t.Fatal(err)
	}

	return in
}

// applyPendingReleases runs one trivial operation so Begin drains whatever the
// cleanups queued.
func applyPendingReleases(t *testing.T, in *Instance) {
	t.Helper()

	if _, err := in.Eval(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
}

// CONTROL: the identical workload with the host never involved. Repeated eval
// does not leak on its own, so anything the host loses is the host's.
func TestMemoryPureGuestEvalLoop(t *testing.T) {
	in := newMemProbe(t)

	base := guestFree(t, in)
	if err := in.Exec(t.Context(), "for _ in range(500):\n    eval('lambda: 1')\n"); err != nil {
		t.Fatal(err)
	}

	delta := guestFree(t, in) - base
	t.Logf("pure guest, 500x eval('lambda: 1'):   delta=%+d", delta)

	if delta < -memSlack {
		t.Errorf("plain MicroPython lost %d bytes; the host is no longer the explanation", -delta)
	}
}

// CONTROL: the same number of host round trips, compiling just as often, but
// a copyable result so no handle is ever minted.
func TestMemoryHostEvalCopyableResult(t *testing.T) {
	in := newMemProbe(t)

	base := guestFree(t, in)
	for range 500 {
		if _, err := in.Eval(t.Context(), "1"); err != nil {
			t.Fatal(err)
		}
	}

	delta := guestFree(t, in) - base
	t.Logf(`host, 500x Eval("1"), no handle:      delta=%+d`, delta)

	if delta < -memSlack {
		t.Errorf("a handle-free round trip cost %d bytes", -delta)
	}
}

// The limitation itself: handles minted faster than Go collects them.
func TestMemoryHostEvalHandleResult(t *testing.T) {
	in := newMemProbe(t)

	base := guestFree(t, in)
	for range 500 {
		if _, err := in.Eval(t.Context(), "lambda: 1"); err != nil {
			t.Fatal(err)
		}
	}

	held := base - guestFree(t, in)

	// Whatever is still held is dominated by the lambdas themselves, at 48
	// bytes each: three 16-byte GC blocks, which is what a function object
	// weighs wherever it is held. The reference table adds about 4 bytes per
	// live slot on top. How much shows up here depends on whether Go's
	// collector happened to run during the loop, so this only logs.
	t.Logf(`host, 500x Eval("lambda: 1"): held=%d bytes (<= %d if none were released)`,
		held, 500*(48+4))

	if held <= 0 {
		t.Fatal("expected deferred release to hold guest memory")
	}
}

// DECISIVE: the same loop with the Go collector forced. What is left is not
// attributable to release timing.
func TestMemoryForcedGCRecoversMost(t *testing.T) {
	in := newMemProbe(t)

	base := guestFree(t, in)
	for range 500 {
		if _, err := in.Eval(t.Context(), "lambda: 1"); err != nil {
			t.Fatal(err)
		}
	}

	stalled := base - guestFree(t, in)

	waitForCleanups(t)
	applyPendingReleases(t, in)
	remaining := base - guestFree(t, in)

	t.Logf("without GC=%d bytes, after a forced GC=%d bytes (recovered %d)",
		stalled, remaining, stalled-remaining)

	if remaining >= stalled {
		t.Error("forcing the Go collector recovered nothing")
	}
}

// The residual scales with PEAK live handles, not total minted: it is the
// reference table's high-water mark, at roughly 4 bytes per slot.
func TestMemoryResidualScalesWithPeak(t *testing.T) {
	var atPeak1, atPeak500 int64

	for _, every := range []int{1, 10, 100, 500} {
		in := newMemProbe(t)

		base := guestFree(t, in)
		for n := range 500 {
			if _, err := in.Eval(t.Context(), "lambda: 1"); err != nil {
				t.Fatal(err)
			}

			if (n+1)%every == 0 {
				waitForCleanups(t)
				applyPendingReleases(t, in)
			}
		}

		waitForCleanups(t)
		applyPendingReleases(t, in)

		held := base - guestFree(t, in)
		t.Logf("500 evals, released every %3d (peak ~%3d live): held=%d bytes",
			every, every, held)

		switch every {
		case 1:
			atPeak1 = held
		case 500:
			atPeak500 = held
		}
	}

	if atPeak1 > memSlack {
		t.Errorf("releasing every iteration still held %d bytes", atPeak1)
	}

	if atPeak500 <= atPeak1 {
		t.Error("residual did not grow with peak concurrent handles")
	}
}

// The failure mode the limitation describes: a loop that looks flat runs the
// guest out of memory, because the releases that would free it sit unqueued.
// The iteration count is a fragmentation threshold rather than a capacity, so
// it is logged, not asserted; that it happens at all is the assertion.
func TestMemoryStalledReleaseExhaustsASmallHeap(t *testing.T) {
	in, err := NewInstance(t.Context(), WithHeapSize(64*1024))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	in.Exec(t.Context(), "import gc")

	for n := range 20000 {
		if _, err := in.Eval(t.Context(), "lambda: 1"); err != nil {
			t.Logf("MemoryError after %d iterations on a 64KB heap: %v", n, err)
			return
		}

		in.Eval(t.Context(), "gc.collect()")
	}

	t.Error("20000 handles minted without a MemoryError: either release stopped" +
		" depending on Go's collector, or the limitation no longer holds")
}

// The other half of the same claim, and what makes the one above meaningful:
// the identical loop on the identical heap runs indefinitely once releases are
// actually applied. Failing here means prompt release stopped being the fix.
func TestMemoryPromptReleaseSurvivesTheSameHeap(t *testing.T) {
	in, err := NewInstance(t.Context(), WithHeapSize(128*1024))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	for n := range 5000 {
		if _, err := in.Eval(t.Context(), "lambda: 1"); err != nil {
			t.Fatalf("failed at iteration %d despite releasing as we go: %v", n, err)
		}

		if (n+1)%50 == 0 {
			waitForCleanups(t)
			applyPendingReleases(t, in)
		}
	}
}
