package host

import (
	"context"
	"encoding/binary"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gregfurman/micropython-go/internal/host/codec"
	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/value"
)

const transferClasses = `
import gc
class Big:
    def __init__(self):
        self.data = bytearray(20000)
class BadRepr:
    def __repr__(self):
        raise ValueError('broken repr')
class LongRepr(Big):
    def __repr__(self):
        return 'x' * 20000
`

func TestFailedTransferReleasesReferences(t *testing.T) {
	for _, tc := range []struct {
		name       string
		expr       string
		arenaFull  bool
		removeHost bool
	}{
		{"result overflow", "[Big(), 'x' * 20000]", true, false},
		{"callback argument overflow", "sink(Big(), 'x' * 20000)", false, false},
		{"callback rejected before decoding", "sink(Big())", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inst := newT(t)
			define(t, inst, "sink", func(context.Context, []value.Value) (value.Value, error) {
				t.Error("callback ran despite failed argument transfer")
				return value.None{}, nil
			})

			if tc.removeHost {
				delete(inst.registry, inst.counter)
			}

			exec(t, inst, transferClasses+"\ndef produce():\n    return "+tc.expr+"\n")

			before := guestHeapFree(t, inst)
			for range 5 {
				inst.Begin()

				_, err := inst.Call("produce", nil)
				if err == nil {
					t.Fatal("transfer succeeded, want failure")
				}

				if tc.arenaFull && !errors.Is(err, memory.ErrArenaFull) {
					t.Fatalf("error = %v, want arena overflow", err)
				}
				// No handle reached Go, so reclamation must not depend on Go GC.
				if free := guestHeapFree(t, inst); free < before-4096 {
					t.Fatalf("failed transfer retained %d guest bytes", before-free)
				}
			}
		})
	}
}

func TestPartialDecodeReleasesTransferReferences(t *testing.T) {
	inst := newT(t)
	// Destroy an object record itself. Cleanup must not depend on recognising
	// its kind, nor on visiting the rest of the value tree.
	exec(t, inst, transferClasses+"\ndef produce():\n    return [Big(), Big(), Big()]\n")
	before := guestHeapFree(t, inst)

	func() {
		defer inst.arena.Mark()()

		name, err := inst.arena.String("produce")
		if err != nil {
			t.Fatal(err)
		}

		out, err := inst.result()
		if err != nil {
			t.Fatal(err)
		}

		used := inst.mod.Xcall(name, 7, 0, 0, out, defaultValueArenaCapacity)
		if err := transferError(used); err != nil {
			t.Fatal(err)
		}

		root, err := inst.mem.View(out, codec.ValueSize)
		if err != nil {
			t.Fatal(err)
		}

		block := int32(binary.LittleEndian.Uint32(root[8:]))

		items, err := inst.mem.View(block, 3*codec.ValueSize)
		if err != nil {
			t.Fatal(err)
		}

		secondID := binary.LittleEndian.Uint32(items[codec.ValueSize+4:])
		thirdID := binary.LittleEndian.Uint32(items[2*codec.ValueSize+4:])
		// The first object gets a Go handle; the other two remain owned only
		// by the transfer and must be released immediately.
		binary.LittleEndian.PutUint32(items[codec.ValueSize:], uint32(codec.KindInvalid))

		if _, err := inst.consumeArena(out, used); err == nil {
			t.Fatal("decoding an invalid kind succeeded")
		}

		for _, id := range []uint32{secondID, thirdID} {
			used = inst.mod.Xref_to_value(int32(id), out, defaultValueArenaCapacity)
			if _, err := inst.consumeArena(out, used); err == nil {
				t.Fatalf("transfer-only reference %d still resolves", id)
			}
		}
	}()
	// The first object was retained by a Go handle before decoding failed. Its
	// cleanup is asynchronous; wait for the actual guest-memory outcome.
	awaitGuestFree(t, inst, before)
}

// A __repr__ that raises or runs long used to fail a transfer, because every
// object crossed with its repr and type name attached. Neither is computed now
// until something asks for it, so an object whose repr misbehaves crosses like
// any other.
func TestUnprintableObjectsCross(t *testing.T) {
	inst := newT(t)
	exec(t, inst, transferClasses)

	for _, expr := range []string{"BadRepr()", "LongRepr()", "[Big(), BadRepr()]"} {
		inst.Begin()

		if _, err := inst.Eval(expr); err != nil {
			t.Errorf("%s: %v", expr, err)
		}
	}
}

// iterator_next reports a byte count like every other result, so the outcomes
// it cannot express that way are the ones worth pinning down: exhaustion writes
// nothing and is not an error, a yielded None writes a result and is not
// exhaustion, and a raise arrives as an ordinary exception value.
func TestIteratorOutcomes(t *testing.T) {
	inst := newT(t)
	exec(t, inst, "def produce():\n    yield None\n    yield 1\n    raise ValueError('stop here')\n")

	v, err := inst.Call("produce", nil)
	if err != nil {
		t.Fatal(err)
	}

	iter := objOf(t, v)

	// A yielded None is a value, not the end of the iterator.
	out, more, err := inst.NextGenerator(iter)
	if err != nil || !more {
		t.Fatalf("yielded None = %v, %v, %v; want None, true, nil", out, more, err)
	}

	if _, ok := out.(value.None); !ok {
		t.Fatalf("yielded None decoded as %#v (%T), want value.None", out, out)
	}

	if out, more, err := inst.NextGenerator(iter); err != nil || !more || out != value.Int(1) {
		t.Fatalf("next item = %v, %v, %v; want 1, true, nil", out, more, err)
	}

	// The raise crosses as an exception value rather than a status.
	out, more, err = inst.NextGenerator(iter)
	if err == nil || more || out != nil {
		t.Fatalf("raising generator = %v, %v, %v; want nil, false, an error", out, more, err)
	}

	exc, ok := errors.AsType[*value.Exception](err)
	if !ok {
		t.Fatalf("err = %v (%T), want a Python exception", err, err)
	}

	if exc.Type() != "ValueError" || !strings.Contains(exc.Message(), "stop here") {
		t.Fatalf("exception = %s(%q), want ValueError(\"stop here\")", exc.Type(), exc.Message())
	}

	// A generator that has raised is finished, and reports that as exhaustion.
	if out, more, err := inst.NextGenerator(iter); err != nil || more || out != nil {
		t.Fatalf("after raising = %v, %v, %v; want nil, false, nil", out, more, err)
	}
}

func TestFailedIteratorTransferReleasesReferences(t *testing.T) {
	inst := newT(t)
	exec(t, inst, transferClasses+"\ndef produce():\n    yield [Big(), 'x' * 20000]\n    yield 42\n")

	v, err := inst.Call("produce", nil)
	if err != nil {
		t.Fatal(err)
	}

	iter := objOf(t, v)

	before := guestHeapFree(t, inst)
	if _, _, err := inst.NextGenerator(iter); !errors.Is(err, memory.ErrArenaFull) {
		t.Fatalf("NextGenerator error = %v, want arena overflow", err)
	}

	if out, more, err := inst.NextGenerator(iter); err != nil || !more || out != value.Int(42) {
		t.Fatalf("next item = %v, %v, %v; want 42, true, nil", out, more, err)
	}

	if _, more, err := inst.NextGenerator(iter); err != nil || more {
		t.Fatalf("iterator exhaustion = %v, %v", more, err)
	}
	// A generator's saved stack can retain its yielded value even after
	// exhaustion. Drop the generator too, leaving only a leaked transfer pin
	// capable of retaining Big.
	iter, v = value.Object{}, nil

	awaitGuestFree(t, inst, before)
}

func TestManyCallbackReferencesAreReleased(t *testing.T) {
	inst := newT(t)
	define(t, inst, "sink", func(context.Context, []value.Value) (value.Value, error) {
		return value.None{}, nil
	})
	exec(t, inst, transferClasses+"\ndef feed():\n    obj = Big()\n    for _ in range(66000):\n        sink(obj)\n")
	before := guestHeapFree(t, inst)
	exec(t, inst, "feed()")
	// No operation boundary occurs within feed, so the guest count exceeds
	// 65,535 even if Go collects every callback argument immediately.
	awaitGuestFree(t, inst, before)
}

func awaitGuestFree(t *testing.T, inst *Module, before int64) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		runtime.GC()

		free := guestHeapFree(t, inst)
		if free >= before-4096 {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("dropped references still retain %d guest bytes", before-free)
		}

		time.Sleep(time.Millisecond)
	}
}
