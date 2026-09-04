package api

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/gregfurman/micropython-go/internal/host"
	"github.com/gregfurman/micropython-go/internal/value"
)

const bigClass = `
import gc

class Big:
    def __init__(self):
        self.data = bytearray(20000)
`

const freeSlack = 4096

func TestRefStaysValidWhileHostHoldsValue(t *testing.T) {
	t.Run("value is still referenced", func(t *testing.T) {
		ctx := context.Background()
		in := newT(t)
		exec(t, in, `greet = lambda: "hello"`)

		v, err := in.Eval(ctx, "greet")
		if err != nil {
			t.Fatal(err)
		}

		gcWait(t)

		out, err := in.CallRef(ctx, objOf(t, v), nil)
		if err != nil {
			t.Fatalf("CallRef on a live handle: %v", err)
		}
		if got := value.Lift(out); got != "hello" {
			t.Errorf("greet() = %#v, want %q", got, "hello")
		}
	})

	t.Run("only a copy of the object survives", func(t *testing.T) {
		ctx := context.Background()
		in := newT(t)
		exec(t, in, `greet = lambda: "hello"`)

		v, err := in.Eval(ctx, "greet")
		if err != nil {
			t.Fatal(err)
		}
		obj := objOf(t, v)
		v = nil

		gcWait(t)

		out, err := in.CallRef(ctx, obj, nil)
		if err != nil {
			t.Fatalf("CallRef through a surviving copy: %v", err)
		}
		if got := value.Lift(out); got != "hello" {
			t.Errorf("greet() = %#v, want %q", got, "hello")
		}
	})
}

func TestObjectFreedAfterHostDropsRef(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	exec(t, in, bigClass)
	exec(t, in, "make = lambda: Big()")

	handle, err := in.Eval(ctx, "make")
	if err != nil {
		t.Fatal(err)
	}

	base := freeBytes(t, in)

	out, err := in.CallRef(ctx, objOf(t, handle), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.(value.Object); !ok {
		t.Fatalf("CallRef returned %T, want an object", out)
	}
	out = nil

	gcWait(t)

	if free := freeBytes(t, in); free < base-freeSlack {
		t.Errorf("guest heap still holds the dropped object: %d bytes free, want ~%d (short by %d)",
			free, base, base-free)
	}
	runtime.KeepAlive(handle)
}

func TestHostFuncArgumentFreed(t *testing.T) {
	in := newT(t)
	define(t, in, "sink", func(context.Context, []value.Value) (value.Value, error) {
		return value.None{}, nil
	})
	exec(t, in, bigClass)
	exec(t, in, "def feed():\n    sink(Big())\n")

	base := freeBytes(t, in)

	exec(t, in, "feed()")
	gcWait(t)

	if free := freeBytes(t, in); free < base-freeSlack {
		t.Errorf("guest heap still holds the argument: %d bytes free, want ~%d (short by %d)",
			free, base, base-free)
	}
}

func TestHandlesToSameObjectAreIndependent(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	exec(t, in, `greet = lambda: "hello"`)

	if _, err := in.Eval(ctx, "greet"); err != nil { // dropped here
		t.Fatal(err)
	}
	second, err := in.Eval(ctx, "greet")
	if err != nil {
		t.Fatal(err)
	}
	kept := objOf(t, second)
	second = nil

	gcWait(t)

	out, err := in.CallRef(ctx, kept, nil)
	if err != nil {
		t.Fatalf("dropping one handle invalidated the other: %v", err)
	}
	if got := value.Lift(out); got != "hello" {
		t.Errorf("greet() = %#v, want %q", got, "hello")
	}
}

func TestRefsAreInvalidatedByRestore(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	snap, err := in.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	exec(t, in, `before = lambda: "before"`)
	old, err := in.Eval(ctx, "before")
	if err != nil {
		t.Fatal(err)
	}
	stale := objOf(t, old)

	if err := in.Restore(ctx, snap); err != nil {
		t.Fatal(err)
	}

	exec(t, in, `after = lambda: "after"`)
	current, err := in.Eval(ctx, "after")
	if err != nil {
		t.Fatal(err)
	}
	kept := objOf(t, current)

	if out, err := in.CallRef(ctx, stale, nil); err == nil {
		t.Errorf("pre-restore ref %d resolved to %#v, want an error", stale.Ref(), value.Lift(out))
	}

	old, stale = nil, value.Object{}
	gcWait(t)

	out, err := in.CallRef(ctx, kept, nil)
	if err != nil {
		t.Fatalf("dropping a pre-restore ref invalidated a live handle: %v", err)
	}
	if got := value.Lift(out); got != "after" {
		t.Errorf("after() = %#v, want %q", got, "after")
	}
}

func TestRestoreDropsSnapshotRefs(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	exec(t, in, `first = lambda: "first"`)

	old, err := in.Eval(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	stale := objOf(t, old)
	snap, err := in.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := in.Restore(ctx, snap); err != nil {
		t.Fatal(err)
	}

	exec(t, in, `second = lambda: "second"`)
	fresh, err := in.Eval(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	current := objOf(t, fresh)
	if current.Ref() != stale.Ref() {
		t.Errorf("fresh ref = %d, want cleared snapshot slot %d", current.Ref(), stale.Ref())
	}

	if _, err := in.CallRef(ctx, stale, nil); !errors.Is(err, host.ErrStaleRef) {
		t.Errorf("CallRef(stale) error = %v, want %v", err, host.ErrStaleRef)
	}
}

func TestRefFromAnotherInstanceIsRejected(t *testing.T) {
	ctx := context.Background()
	source := newT(t)
	target := newT(t)

	exec(t, source, `fn = lambda: "from source"`)
	foreign, err := source.Eval(ctx, "fn")
	if err != nil {
		t.Fatal(err)
	}

	exec(t, target, `fn = lambda: "from target"`)
	local, err := target.Eval(ctx, "fn")
	if err != nil {
		t.Fatal(err)
	}
	if objOf(t, foreign).Ref() == objOf(t, local).Ref() {
		t.Logf("both instances minted id %d", objOf(t, foreign).Ref())
	}

	exec(t, target, "def call(f):\n    return f()\n")
	if out, err := target.Call(ctx, "call", foreign); err == nil {
		t.Fatalf("foreign ref resolved to %#v, want an error", value.Lift(out))
	}
	if _, err := target.CallRef(ctx, objOf(t, foreign), nil); err == nil {
		t.Fatal("CallRef accepted a foreign ref")
	}
}

func TestReleaseDoesNotRaceWithGuestCall(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	exec(t, in, `fn = lambda: "hello"`)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()

	for range 200 {
		v, err := in.Eval(ctx, "fn")
		if err != nil {
			t.Fatalf("Eval during concurrent collection: %v", err)
		}
		if _, err := in.CallRef(ctx, objOf(t, v), nil); err != nil {
			t.Fatalf("CallRef during concurrent collection: %v", err)
		}
	}
}

func TestRefTableDoesNotGrowInGuestLoop(t *testing.T) {
	in := newT(t)
	define(t, in, "sink", func(context.Context, []value.Value) (value.Value, error) {
		return value.None{}, nil
	})
	exec(t, in, "import gc\nclass Thing:\n    pass\n")
	exec(t, in, "def loop(n):\n    t = Thing()\n    for _ in range(n):\n        sink(t)\n")

	base := freeBytes(t, in)
	exec(t, in, "loop(20000)")

	if free := freeBytes(t, in); free < base-freeSlack {
		t.Errorf("ref table grew by %d bytes across the loop", base-free)
	}
}

// ----

func newT(t *testing.T) *Instance {
	t.Helper()
	in, err := New(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close() })
	return in
}

func define(t *testing.T, in *Instance, name string, fn host.HostFunc) {
	t.Helper()
	if err := in.DefineFunction(context.Background(), name, fn); err != nil {
		t.Fatalf("DefineFunction(%q): %v", name, err)
	}
}

func exec(t *testing.T, in *Instance, src string) {
	t.Helper()
	if err := in.Exec(context.Background(), src); err != nil {
		t.Fatalf("Exec(%q): %v", src, err)
	}
}

func eval(t *testing.T, in *Instance, expr string) any {
	t.Helper()
	got, err := in.Eval(context.Background(), expr)
	if err != nil {
		t.Fatalf("Eval(%q): %v", expr, err)
	}
	if got == nil {
		return nil
	}
	return value.Lift(got)
}

func freeBytes(t *testing.T, in *Instance) int64 {
	t.Helper()
	exec(t, in, "gc.collect()")
	free, ok := eval(t, in, "gc.mem_free()").(int64)
	if !ok {
		t.Fatal("gc.mem_free() did not return an int")
	}
	return free
}

func objOf(t *testing.T, v value.Value) value.Object {
	t.Helper()
	o, ok := v.(value.Object)
	if !ok {
		t.Fatalf("value %#v (%T) is not an object", v, v)
	}
	if o.Ref() == 0 {
		t.Fatal("object carries ref 0, so it is not bound to the guest")
	}
	return o
}

func gcWait(t *testing.T) {
	t.Helper()
	for range 3 {
		done := make(chan struct{})
		func() {
			// Not new(int): the tiny allocator batches those, and they hold
			// each other's cleanups back.
			sentinel := new([64]byte)
			runtime.AddCleanup(sentinel, func(ch chan struct{}) { close(ch) }, done)
		}()
		runtime.GC()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("cleanup queue did not drain")
		}
	}
}
