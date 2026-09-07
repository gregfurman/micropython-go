package memory

import (
	"bytes"
	"errors"
	"testing"
)

// fakeAlloc is a bump allocator over the tail of a memory, standing in for the
// guest's malloc. Freed pointers are recorded rather than reused, so a test can
// assert on what an arena released.
type fakeAlloc struct {
	next  int32
	freed []int32
	fail  bool
}

func (f *fakeAlloc) Xmalloc(n int32) int32 {
	if f.fail {
		return 0
	}
	ptr := f.next
	f.next += (n + 7) &^ 7
	return ptr
}

func (f *fakeAlloc) Xfree(ptr int32) { f.freed = append(f.freed, ptr) }

func newTestMemory(t *testing.T) (*Memory, *fakeAlloc) {
	t.Helper()
	m := New(1, MaxPages)
	a := &fakeAlloc{next: 64}
	m.Bind(a)
	return m, a
}

func TestArenaBumpsAndAligns(t *testing.T) {
	m, _ := newTestMemory(t)
	arena, err := m.NewArena(256)
	if err != nil {
		t.Fatal(err)
	}

	first, err := arena.New(1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := arena.New(4)
	if err != nil {
		t.Fatal(err)
	}

	if got := second - first; got != 4 {
		t.Errorf("a 1-byte allocation advanced the arena by %d bytes, want 4", got)
	}
	if second%4 != 0 {
		t.Errorf("allocation at %d is not 4-byte aligned", second)
	}
}

func TestBorrowedArenaViewBounds(t *testing.T) {
	m, _ := newTestMemory(t)
	arena := m.ArenaAt(64, 32)
	for _, tc := range []struct {
		ptr, size int32
		valid     bool
	}{
		{64, 32, true}, {96, 0, true}, {0, 0, true},
		{60, 4, false}, {64, 33, false}, {97, 0, false},
		{64, -1, false}, {0, 1, false}, {1<<31 - 1, 4, false},
	} {
		_, err := arena.View(tc.ptr, tc.size)
		if (err == nil) != tc.valid {
			t.Errorf("View(%d, %d) error = %v, valid = %v", tc.ptr, tc.size, err, tc.valid)
		}
	}
}

func TestArenaZeroSizeTakesNoSpace(t *testing.T) {
	m, alloc := newTestMemory(t)
	arena, err := m.NewArena(64)
	if err != nil {
		t.Fatal(err)
	}

	before := arena.next
	if _, err := arena.New(0); err != nil {
		t.Fatal(err)
	}
	if arena.next != before {
		t.Errorf("a zero-byte allocation moved the bump pointer to %d, want %d", arena.next, before)
	}
	if len(arena.ptrs) != 0 {
		t.Errorf("a zero-byte allocation spilled to the heap: %v", arena.ptrs)
	}

	ptr, err := arena.Bytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	if ptr != 0 {
		t.Errorf("Bytes(nil) = %d, want the null pointer", ptr)
	}
	if alloc.next != 64+64 {
		t.Errorf("the guest allocator was called for an empty payload")
	}
}

// The bug this covers: writing records through Memory.Read, which returns a
// copy, left guest memory untouched.
func TestArenaWritesReachGuestMemory(t *testing.T) {
	m, _ := newTestMemory(t)
	arena, err := m.NewArena(256)
	if err != nil {
		t.Fatal(err)
	}

	ptr, err := arena.Bytes([]byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Read(ptr, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("payload")) {
		t.Errorf("read back %q, want %q", got, "payload")
	}

	view, err := arena.View(ptr, 7)
	if err != nil {
		t.Fatal(err)
	}
	copy(view, "changed")
	if got, _ := m.ReadString(ptr, 7); got != "changed" {
		t.Errorf("a write through View did not land in memory: %q", got)
	}
}

func TestArenaCStringTerminates(t *testing.T) {
	m, _ := newTestMemory(t)
	arena, err := m.NewArena(64)
	if err != nil {
		t.Fatal(err)
	}

	ptr, err := arena.CString("hi")
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Read(ptr, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{'h', 'i', 0}) {
		t.Errorf("CString wrote %v, want %v", got, []byte{'h', 'i', 0})
	}
}

func TestArenaMarkRewindsAndFreesSpills(t *testing.T) {
	m, alloc := newTestMemory(t)
	arena, err := m.NewArena(32)
	if err != nil {
		t.Fatal(err)
	}

	reset := arena.Mark()
	inside, err := arena.New(16)
	if err != nil {
		t.Fatal(err)
	}
	spilled, err := arena.New(1024) // larger than the span, so it goes to the heap
	if err != nil {
		t.Fatal(err)
	}
	if len(arena.ptrs) != 1 {
		t.Fatalf("an oversized allocation did not spill: %v", arena.ptrs)
	}

	reset()

	if len(alloc.freed) != 1 || alloc.freed[0] != spilled {
		t.Errorf("reset freed %v, want just the spill at %d", alloc.freed, spilled)
	}
	if len(arena.ptrs) != 0 {
		t.Errorf("reset left %d spills tracked", len(arena.ptrs))
	}

	again, err := arena.New(16)
	if err != nil {
		t.Fatal(err)
	}
	if again != inside {
		t.Errorf("after reset the arena handed out %d, want %d again", again, inside)
	}
}

// Free must release every spill exactly once, however many Marks came and went.
func TestArenaFreeReleasesSpansOnceEach(t *testing.T) {
	m, alloc := newTestMemory(t)
	arena, err := m.NewArena(16)
	if err != nil {
		t.Fatal(err)
	}
	base := arena.base

	spilled, err := arena.New(1024)
	if err != nil {
		t.Fatal(err)
	}

	arena.Free()
	arena.Free() // idempotent

	want := map[int32]int{spilled: 1, base: 1}
	got := map[int32]int{}
	for _, ptr := range alloc.freed {
		got[ptr]++
	}
	for ptr, n := range want {
		if got[ptr] != n {
			t.Errorf("pointer %d freed %d times, want %d", ptr, got[ptr], n)
		}
	}
	if len(alloc.freed) != 2 {
		t.Errorf("Free released %v, want exactly the span and its spill", alloc.freed)
	}
}

// A region the host does not own cannot spill: a heap pointer would outlive the
// call that lent the region and nothing would free it.
func TestViewingArenaReportsFullInsteadOfSpilling(t *testing.T) {
	m, alloc := newTestMemory(t)
	arena := m.ArenaAt(128, 16)

	if _, err := arena.New(16); err != nil {
		t.Fatalf("an allocation that fits the region failed: %v", err)
	}
	_, err := arena.New(1)
	if !errors.Is(err, ErrArenaFull) {
		t.Errorf("overflowing a viewed region gave %v, want ErrArenaFull", err)
	}
	if alloc.next != 64 {
		t.Error("a viewed region spilled to the guest heap")
	}

	arena.Free()
	if len(alloc.freed) != 0 {
		t.Errorf("Free released %v on a region the arena does not own", alloc.freed)
	}
}

func TestArenaRejectsNegativeSize(t *testing.T) {
	m, _ := newTestMemory(t)
	arena, err := m.NewArena(16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := arena.New(-1); !errors.Is(err, ErrInvalidMemory) {
		t.Errorf("New(-1) = %v, want ErrInvalidMemory", err)
	}
}

func TestArenaSaveAndLoadResumeThePosition(t *testing.T) {
	m, alloc := newTestMemory(t)
	arena, err := m.NewArena(64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := arena.New(16); err != nil {
		t.Fatal(err)
	}
	if _, err := arena.New(1024); err != nil { // a spill, dropped by Load
		t.Fatal(err)
	}
	state := arena.Save()

	restored := &Arena{}
	restored.Load(m, state)

	next, err := restored.New(4)
	if err != nil {
		t.Fatal(err)
	}
	if want := arena.base + 16; next != want {
		t.Errorf("restored arena handed out %d, want %d", next, want)
	}
	if len(alloc.freed) != 0 {
		t.Errorf("Load freed %v; spills belong to the rewound image", alloc.freed)
	}
}

func TestArenaSurfacesGuestOOM(t *testing.T) {
	m, alloc := newTestMemory(t)
	arena, err := m.NewArena(8)
	if err != nil {
		t.Fatal(err)
	}

	alloc.fail = true
	if _, err := arena.New(1024); !errors.Is(err, ErrGuestOOM) {
		t.Errorf("a failed spill gave %v, want ErrGuestOOM", err)
	}
}
