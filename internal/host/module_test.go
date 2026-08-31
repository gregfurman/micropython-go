package host

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/gregfurman/micropython-go/internal/host/codec"
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
// api.run does before every operation.
func eval(t *testing.T, inst *Module, expr string) any {
	t.Helper()
	inst.Begin()
	got, err := inst.Eval(expr)
	if err != nil {
		t.Fatalf("Eval(%q): %v", expr, err)
	}
	return got
}

func TestKindsMatchGuest(t *testing.T) {
	want := []codec.Kind{
		codec.KindException, codec.KindNull, codec.KindNone, codec.KindBool,
		codec.KindInt, codec.KindBigint, codec.KindFloat, codec.KindStr,
		codec.KindBytes, codec.KindCallable, codec.KindObject, codec.KindRef,
	}
	inst := newT(t)
	for i, w := range want {
		if got := codec.Kind(inst.mod.Xkind_of(int32(i))); got != w {
			t.Errorf("slot %d: guest=%d go=%d", i, got, w)
		}
	}
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
			fn: func(args []any) (any, error) {
				s, ok := args[0].(string)
				if !ok {
					return nil, fmt.Errorf("want str, got %T", args[0])
				}
				return strings.ToUpper(s), nil
			},
			expr: `f("hello world")`,
			want: "HELLO WORLD",
		},
		{
			name: "ints add",
			fn: func(args []any) (any, error) {
				a, ok := args[0].(int64)
				if !ok {
					return nil, fmt.Errorf("arg 0: want int64, got %T", args[0])
				}
				b, ok := args[1].(int64)
				if !ok {
					return nil, fmt.Errorf("arg 1: want int64, got %T", args[1])
				}
				return a + b, nil
			},
			expr: `f(1, 2)`,
			want: int64(3),
		},
		{
			name: "no arguments",
			fn:   func([]any) (any, error) { return "constant", nil },
			expr: `f()`,
			want: "constant",
		},
		{
			name: "returns a dict the guest can index",
			fn:   func([]any) (any, error) { return map[string]any{"key_1": "val_1"}, nil },
			expr: `f()["key_1"]`,
			want: "val_1",
		},
		{
			name: "returns None",
			fn:   func([]any) (any, error) { return nil, nil },
			expr: `f() is None`,
			want: true,
		},
		{
			name: "arguments arrive already decoded",
			fn: func(args []any) (any, error) {
				return fmt.Sprintf("%T/%T/%T", args[0], args[1], args[2]), nil
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
			fn:       func([]any) (any, error) { return nil, errors.New("boom") },
			wantType: "HostError",
			wantMsg:  "boom",
		},
		{
			name:     "typed error raises that builtin",
			fn:       func([]any) (any, error) { return nil, value.NewException("ValueError", "bad input") },
			wantType: "ValueError",
			wantMsg:  "bad input",
		},
		{
			name:     "unknown class falls back to HostError",
			fn:       func([]any) (any, error) { return nil, value.NewException("NoSuchError", "hm") },
			wantType: "HostError",
			wantMsg:  "hm",
		},
		{
			name:     "panic is recovered, not propagated",
			fn:       func([]any) (any, error) { panic("kaboom") },
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
			pyErr, ok := errors.AsType[*codec.PythonError](err)
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
	define(t, inst, "f", func([]any) (any, error) { return "from host", nil })

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

	define(t, inst, "payload", func([]any) (any, error) {
		return map[string]any{
			"empty_string": "",
			"empty_bytes":  []byte{},
			"nested":       []any{"x", []byte{1, 2}},
			"big":          new(big.Int).Lsh(big.NewInt(1), 63),
		}, nil
	})
	define(t, inst, "identity", func(args []any) (any, error) { return args[0], nil })

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
