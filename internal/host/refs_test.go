package host

import (
	"errors"
	"math"
	"runtime"
	"testing"
)

func TestGuestReleaseOnlyUpdatesCount(t *testing.T) {
	refs := &OwnedReferences{release: func(uint32) {
		t.Fatal("guest-initiated release called back into the guest")
	}}
	inst := &Module{refs: refs}
	id := inst.Xgo_ref_add(64)
	if again := inst.Xgo_ref_add(64); again != id {
		t.Fatalf("same address received IDs %d and %d", id, again)
	}
	if got := inst.Xgo_ref_free(id); got != 0 {
		t.Fatalf("first release = %d, want 0 (still pinned)", got)
	}
	if got := inst.Xgo_ref_free(id); got != 1 {
		t.Fatalf("last release = %d, want 1 (C must unpin)", got)
	}
	if got := inst.Xgo_ref_free(id); got != 0 {
		t.Fatalf("stale release = %d, want 0", got)
	}
}

func TestRetainedHandleOutlivesGuestAcquisition(t *testing.T) {
	unpinned := 0
	refs := &OwnedReferences{release: func(uint32) { unpinned++ }}
	id := uint32(refs.Acquire(64))
	ref, err := refs.Retain(id)
	if err != nil {
		t.Fatal(err)
	}
	if refs.Release(id) || unpinned != 0 {
		t.Fatal("releasing the guest acquisition unpinned a live Go handle")
	}
	if got, err := refs.Lookup(ref); err != nil || got != id {
		t.Fatalf("retained handle lookup = %d, %v", got, err)
	}
	if _, err := (&OwnedReferences{}).Lookup(ref); !errors.Is(err, ErrStaleRef) {
		t.Fatalf("foreign owner accepted handle: %v", err)
	}
	// Simulate the queued cleanup to check the final-release path directly.
	refs.pending = append(refs.pending, id)
	refs.Drain()
	if unpinned != 1 {
		t.Fatalf("unpinned %d times, want once", unpinned)
	}
	if _, err := refs.Retain(id); !errors.Is(err, ErrStaleRef) {
		t.Fatalf("retaining released ID: %v", err)
	}
	fresh := uint32(refs.Acquire(96))
	if fresh == id || fresh&refIndexMask != id&refIndexMask {
		t.Fatalf("reused slot ID = %d, old ID = %d", fresh, id)
	}
	if refs.Release(id) || unpinned != 1 {
		t.Fatal("old generation released the new slot")
	}
	refs.Release(fresh)
	runtime.KeepAlive(ref)
}

func TestReferenceCountsDoNotOverflow(t *testing.T) {
	refs := &OwnedReferences{}
	id := uint32(refs.Acquire(64))
	index := id & refIndexMask
	refs.slots[index].count = math.MaxUint32
	if got := refs.Acquire(64); got != invalidRefID {
		t.Fatalf("Acquire at maximum count = %d", got)
	}
	if _, err := refs.Retain(id); !errors.Is(err, ErrRefOverflow) {
		t.Fatalf("Retain at maximum count: %v", err)
	}
	if refs.slots[index].count != math.MaxUint32 {
		t.Fatal("failed acquisition changed count")
	}
}
