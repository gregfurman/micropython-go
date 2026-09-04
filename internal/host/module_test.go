package host

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gregfurman/micropython-go/internal/value"
)

const recordRaisedClass = `
try:
    f()
except Exception as e:
    seen = type(e).__name__
`

func newT(t *testing.T) *Module {
	t.Helper()
	inst, err := NewModule(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	return inst
}

// define registers fn and fails the test if the guest rejects it.
func define(t *testing.T, inst *Module, name string, fn HostFunc) {
	t.Helper()
	if err := inst.DefineFunction(name, fn); err != nil {
		t.Fatalf("DefineFunction(%q): %v", name, err)
	}
}

// eval evaluates expr and fails the test if the guest raises. Begin mirrors what
// api.run does before every operation. The result is lifted to the Go value it
// stands for, which is what these tests assert on.
func eval(t *testing.T, inst *Module, expr string) any {
	t.Helper()
	step(t, inst)
	got, err := inst.Eval(expr)
	if err != nil {
		t.Fatalf("Eval(%q): %v", expr, err)
	}
	if got == nil {
		return nil
	}
	return value.Lift(got)
}

func TestHostFunc(t *testing.T) {
	tests := []struct {
		name string
		fn   HostFunc
		expr string
		want any
	}{
		{
			name: "string in and out",
			fn: func(_ context.Context, args []value.Value) (value.Value, error) {
				s, ok := value.Lift(args[0]).(string)
				if !ok {
					return nil, fmt.Errorf("want str, got %T", value.Lift(args[0]))
				}
				return value.Str(strings.ToUpper(s)), nil
			},
			expr: `f("hello world")`,
			want: "HELLO WORLD",
		},
		{
			name: "ints add",
			fn: func(_ context.Context, args []value.Value) (value.Value, error) {
				a, ok := value.Lift(args[0]).(int64)
				if !ok {
					return nil, fmt.Errorf("arg 0: want int64, got %T", value.Lift(args[0]))
				}
				b, ok := value.Lift(args[1]).(int64)
				if !ok {
					return nil, fmt.Errorf("arg 1: want int64, got %T", value.Lift(args[1]))
				}
				return value.Int(a + b), nil
			},
			expr: `f(1, 2)`,
			want: int64(3),
		},
		{
			name: "no arguments",
			fn:   func(context.Context, []value.Value) (value.Value, error) { return value.Str("constant"), nil },
			expr: `f()`,
			want: "constant",
		},
		{
			name: "returns a dict the guest can index",
			fn: func(context.Context, []value.Value) (value.Value, error) {
				return value.NewDict(value.Item{Key: value.Str("key_1"), Val: value.Str("val_1")}), nil
			},
			expr: `f()["key_1"]`,
			want: "val_1",
		},
		{
			name: "returns None",
			fn:   func(context.Context, []value.Value) (value.Value, error) { return value.None{}, nil },
			expr: `f() is None`,
			want: true,
		},
		{
			name: "arguments arrive already decoded",
			fn: func(_ context.Context, args []value.Value) (value.Value, error) {
				return value.Str(fmt.Sprintf("%T/%T/%T", value.Lift(args[0]), value.Lift(args[1]), value.Lift(args[2]))), nil
			},
			expr: `f("s", 1, b"xy")`,
			want: "string/int64/[]uint8",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inst := newT(t)
			define(t, inst, "f", tc.fn)
			if got := eval(t, inst, tc.expr); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s = %#v (%T), want %#v (%T)", tc.expr, got, got, tc.want, tc.want)
			}
		})
	}
}

func TestHostFuncErrors(t *testing.T) {
	tests := []struct {
		name     string
		fn       HostFunc
		wantType string
		wantMsg  string
	}{
		{
			name:     "plain error falls back to HostError",
			fn:       func(context.Context, []value.Value) (value.Value, error) { return nil, errors.New("boom") },
			wantType: "HostError",
			wantMsg:  "boom",
		},
		{
			name: "typed error raises that builtin",
			fn: func(context.Context, []value.Value) (value.Value, error) {
				return nil, value.NewException("ValueError", "bad input")
			},
			wantType: "ValueError",
			wantMsg:  "bad input",
		},
		{
			name: "unknown class falls back to HostError",
			fn: func(context.Context, []value.Value) (value.Value, error) {
				return nil, value.NewException("NoSuchError", "hm")
			},
			wantType: "HostError",
			wantMsg:  "hm",
		},
		{
			name:     "panic is recovered, not propagated",
			fn:       func(context.Context, []value.Value) (value.Value, error) { panic("kaboom") },
			wantType: "HostError",
			wantMsg:  "host function panicked: kaboom",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inst := newT(t)
			define(t, inst, "f", tc.fn)

			got, err := inst.Eval(`f()`)
			if got != nil {
				t.Errorf("value = %#v, want nil on error", got)
			}
			pyErr, ok := errors.AsType[*value.Exception](err)
			if !ok {
				t.Fatalf("err = %v (%T), want *codec.PythonError", err, err)
			}
			if pyErr.Type() != tc.wantType {
				t.Errorf("exception type = %q, want %q", pyErr.Type(), tc.wantType)
			}
			if !strings.Contains(pyErr.Message(), tc.wantMsg) {
				t.Errorf("message = %q, want it to contain %q", pyErr.Message(), tc.wantMsg)
			}

			if err := inst.Exec(recordRaisedClass); err != nil {
				t.Fatal(err)
			}
			if got := eval(t, inst, "seen"); got != tc.wantType {
				t.Errorf("guest saw %#v, want %q", got, tc.wantType)
			}

			// The interpreter must stay usable after a host function fails.
			if got := eval(t, inst, `1 + 1`); got != int64(2) {
				t.Errorf("instance unusable after a raise: 1+1 = %#v", got)
			}
		})
	}
}

func TestDefineFunctionRejectsNil(t *testing.T) {
	if err := newT(t).DefineFunction("f", nil); err == nil {
		t.Fatal("DefineFunction(nil) = nil, want error")
	}
}

func TestHostFuncSurvivesSnapshotRestore(t *testing.T) {
	inst := newT(t)
	define(t, inst, "f", func(context.Context, []value.Value) (value.Value, error) { return value.Str("from host"), nil })

	snap := inst.Snapshot()

	t.Run("into a fresh instance", func(t *testing.T) {
		restored, err := snap.Restore()
		if err != nil {
			t.Fatal(err)
		}
		if got := eval(t, restored, `f()`); got != "from host" {
			t.Errorf("f() = %#v after Snapshot.Restore", got)
		}
	})

	t.Run("into the same instance", func(t *testing.T) {
		if err := inst.Restore(snap); err != nil {
			t.Fatal(err)
		}
		if got := eval(t, inst, `f()`); got != "from host" {
			t.Errorf("f() = %#v after Module.Restore", got)
		}
	})
}

func TestValuesRoundTrip(t *testing.T) {
	inst := newT(t)

	define(t, inst, "payload", func(context.Context, []value.Value) (value.Value, error) {
		return value.NewDict(
			value.Item{Key: value.Str("empty_string"), Val: value.Str("")},
			value.Item{Key: value.Str("empty_bytes"), Val: value.Bytes{}},
			value.Item{Key: value.Str("nested"), Val: value.NewList(value.Str("x"), value.Bytes{1, 2})},
			value.Item{Key: value.Str("big"), Val: value.NewBigInt(new(big.Int).Lsh(big.NewInt(1), 63))},
		), nil
	})
	define(t, inst, "identity", func(_ context.Context, args []value.Value) (value.Value, error) { return args[0], nil })

	t.Run("host to guest", func(t *testing.T) {
		expr := `payload() == {"empty_string": "", "empty_bytes": b"", "nested": ["x", b"\x01\x02"], "big": 9223372036854775808}`
		if got := eval(t, inst, expr); got != true {
			t.Errorf("host payload did not round-trip: %v", got)
		}
	})

	t.Run("guest to host and back", func(t *testing.T) {
		expr := `identity(["argument", {"nested": b"value"}]) == ["argument", {"nested": b"value"}]`
		if got := eval(t, inst, expr); got != true {
			t.Errorf("host argument did not round-trip: %v", got)
		}
	})

	t.Run("guest to host", func(t *testing.T) {
		got := eval(t, inst, `["", b"", ["nested"], {"key": "value"}]`)
		want := []any{"", []byte{}, []any{"nested"}, map[string]any{"key": "value"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("guest payload mismatch:\n got: %#v\nwant: %#v", got, want)
		}
	})
}

func TestGarbageCollection(t *testing.T) {
	inst := newT(t)

	fn := func(_ context.Context, _ []value.Value) (value.Value, error) {
		return value.Str("pong"), nil
	}

	err := inst.DefineFunction("ping", fn)
	if err != nil {
		t.Fatalf("unexpected error when defining function: %v", err)
	}

	val, err := inst.Call("ping", []any{})
	if err != nil {
		t.Fatalf("unexpected error when calling 'ping' function: %v", err)
	}

	sval := value.Lift(val).(string)
	if sval != "pong" {
		t.Fatalf("unexpected response from 'ping'. Wanted %q, got %q", "pong", sval)
	}

	inst.registry[0] = nil

	runtime.GC()

	val, err = inst.Call("ping", []any{})
	if err != nil {
		t.Fatalf("unexpected error when calling 'ping' function: %v", err)
	}

}

// ---------------------------------------------------------------------
// Reference lifetime
//
// Ids handed to the host are released from whichever goroutine holds the
// interpreter; internal/api/instance_test.go covers that lifecycle end to end.
// What is left here needs to reach inside the Module to forge a ref.
// ---------------------------------------------------------------------

// step mirrors what api.run does before every operation: it clears a cancel
// left over from the last one and frees the refs the host has dropped since.
func step(t *testing.T, inst *Module) {
	t.Helper()
	inst.Begin()
	inst.GC()
}

func exec(t *testing.T, inst *Module, src string) {
	t.Helper()
	step(t, inst)
	if err := inst.Exec(src); err != nil {
		t.Fatalf("Exec(%q): %v", src, err)
	}
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

// gcWait forces collection and waits for the runtime to drain the cleanup
// queue, using a sentinel cleanup as the barrier. Cleanup ordering is not
// specified, so it runs a few rounds.
func gcWait(t *testing.T) {
	t.Helper()
	for range 3 {
		done := make(chan struct{})
		func() {
			// Not new(int): objects the tiny allocator batches can hold each
			// other's cleanups back.
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

// A released id can be handed out again for a different object, so an id that
// outlives its handle must be rejected rather than resolve to the new one.
func TestReleasedRefIDIsNotReused(t *testing.T) {
	inst := newT(t)
	exec(t, inst, `first = lambda: "first"`)

	step(t, inst)
	handle, err := inst.Eval("first")
	if err != nil {
		t.Fatal(err)
	}
	staleID := objOf(t, handle).Ref()
	handle = nil

	gcWait(t)

	exec(t, inst, `second = lambda: "second"`)
	step(t, inst)
	fresh, err := inst.Eval("second")
	if err != nil {
		t.Fatal(err)
	}
	if objOf(t, fresh).Ref() == staleID {
		t.Errorf("id %d was handed straight back out", staleID)
	}

	stale := value.NewObject("function", "<stale>", inst.refs.Track(staleID), false, true)
	step(t, inst)
	if out, err := inst.CallRef(stale, nil); err == nil {
		t.Fatalf("stale ref %d resolved to %#v, want an error", staleID, value.Lift(out))
	}
}
