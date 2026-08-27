package impl

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/gregfurman/micropython-go/internal/minimal/impl/codec"
)

func TestKinds(t *testing.T) {
	inst, err := NewInstance(0)
	if err != nil {
		panic(err)
	}

	want := []codec.Kind{codec.KindException, codec.KindNull, codec.KindNone, codec.KindBool, codec.KindInt,
		codec.KindBigint, codec.KindFloat, codec.KindStr, codec.KindBytes,
		codec.KindCallable, codec.KindObject, codec.KindRef}
	for i, w := range want {
		if got := codec.Kind(inst.mod.Xkind_of(int32(i))); got != w {
			t.Errorf("slot %d: guest=%d go=%d", i, got, w)
		}
	}
}

func TestEval(t *testing.T) {
	inst, err := NewInstance(0)
	if err != nil {
		panic(err)
	}

	inst.DefineFunction("louder", func(args []any) (any, error) {
		s, ok := args[0].(string)
		if !ok {
			return "", fmt.Errorf("louder: want str, got %T", args[0])
		}
		return strings.ToUpper(s), nil
	})

	val, err := inst.Eval(`louder("hello world")`)
	if err != nil {
		panic(err)
	}

	fmt.Printf("msg = %v\n", val)
}

func TestEvalInt(t *testing.T) {
	inst, err := NewInstance(0)
	if err != nil {
		t.Fatal(err.Error())
	}

	inst.DefineFunction("add", func(args []any) (any, error) {
		a, ok := args[0].(int32)
		if !ok {
			return "", fmt.Errorf("louder: want int32, got %T", args[0])
		}

		b, ok := args[1].(int32)
		if !ok {
			return "", fmt.Errorf("louder: want int32, got %T", args[0])
		}

		return a + b, nil
	})

	val, err := inst.Eval(`add(1, 2)`)
	if err != nil {
		t.Fatal(err.Error())
	}

	if val.(int32) != 3 {
		t.Fatalf("expected add(1, 2) to return 3, got %d", val)
	}
}

func TestEvalErr(t *testing.T) {
	inst, err := NewInstance(0)
	if err != nil {
		t.Fatal(err.Error())
	}

	inst.DefineFunction("fails", func(args []any) (any, error) {
		return nil, errors.New("fails")
	})

	val, err := inst.Eval(`fails()`)

	if val != nil {
		t.Fatalf("val to be empty, got %v", val)
	}

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	pyErr, ok := errors.AsType[*codec.PythonError](err)
	if !ok {
		t.Fatal("expected error to be PythonError")
	}

	if pyErr.Type != "HostError" {
		t.Fatal("expected raised exception to be a HostError")
	}

}

func TestEvalDict(t *testing.T) {
	inst, err := NewInstance(0)
	if err != nil {
		panic(err)
	}

	inst.DefineFunction("structured", func(args []any) (any, error) {
		return map[any]any{1: "val_1"}, nil
	})

	val, err := inst.Eval(`structured().get("key_1")`)
	if err != nil {
		panic(err)
	}

	fmt.Printf("val: %v\n", val)

	val, err = inst.Eval(`structured()`)
	if err != nil {
		panic(err)
	}

	fmt.Printf("val: %v\n", val)

	val, err = inst.Eval(`{"foo":"bar"}`)
	if err != nil {
		panic(err)
	}

	fmt.Printf("val: %v\n", val)

}

func TestValuesCrossHostBoundary(t *testing.T) {
	inst, err := NewInstance(0)
	if err != nil {
		t.Fatal(err)
	}

	err = inst.DefineFunction("payload", func([]any) (any, error) {
		return map[string]any{
			"empty_string": "",
			"empty_bytes":  []byte{},
			"nested":       []any{"x", []byte{1, 2}},
			"big":          new(big.Int).Lsh(big.NewInt(1), 63),
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = inst.DefineFunction("identity", func(args []any) (any, error) {
		return args[0], nil
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := inst.Eval(`payload() == {"empty_string": "", "empty_bytes": b"", "nested": ["x", b"\x01\x02"], "big": 9223372036854775808}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != true {
		t.Fatalf("host payload did not round-trip: %v", got)
	}
	got, err = inst.Eval(`identity(["argument", {"nested": b"value"}]) == ["argument", {"nested": b"value"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if got != true {
		t.Fatalf("host argument did not round-trip: %v", got)
	}

	got, err = inst.Eval(`["", b"", ["nested"], {"key": "value"}]`)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"", []byte{}, []any{"nested"}, map[any]any{"key": "value"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("guest payload mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
