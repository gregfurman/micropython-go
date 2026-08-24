package micropython_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	micropython "github.com/gregfurman/micropython-go"
)

func TestSets(t *testing.T) {
	in, err := micropython.NewInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	for _, expr := range []string{
		"{1, 2, 3}",
		"{1, 2} | {2, 3}",
		"{1, 2, 3} - {2}",
		"{1, 2, 3} & {2, 3, 4}",
		"set()",
		"frozenset({1, 2})",
		"sorted({3, 1, 2})",
		"len({1, 2, 2, 3})",
		"2 in {1, 2}",
		"{'a', 'b'}",
	} {
		_, err := in.Eval(t.Context(), expr)
		if err != nil {
			t.Fatalf("Failed to evaluate expression %s: %s", expr, err)
			continue
		}
	}

	if _, err := in.Exec(t.Context(), "def echo(v):\n    return v\ndef kind(v):\n    return type(v).__name__\n"); err != nil {
		t.Fatal(err)
	}

	for _, in0 := range []any{
		micropython.Set(micropython.Int(1), micropython.Int(2), micropython.Int(3)),
		micropython.Set(micropython.Str("a"), micropython.Str("b")),
		micropython.FrozenSet(micropython.Int(7)),
		micropython.Set(),
	} {
		got, err := in.Call(t.Context(), "echo", in0)
		if err != nil {
			t.Errorf("echo(%#v) -> %v", in0, err)
			continue
		}
		k, _ := in.Call(t.Context(), "kind", in0)
		t.Logf("%-34s -> python %-10v -> %T %v", fmt.Sprintf("%#v", in0), k, got, got)
	}

	// Python sets cannot contain unhashable types like lists
	_, err = in.Call(t.Context(), "echo", micropython.Set(micropython.List(micropython.Int(1))))
	t.Logf("set containing a list -> %v", err)
	if in.Err() != nil {
		t.Errorf("instance died: %v", in.Err())
	}
}
func TestFrozenSetIsImmutable(t *testing.T) {
	in, err := micropython.NewInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	if _, err := in.Exec(t.Context(), "def mutate(v):\n    v.add(99)\n"); err != nil {
		t.Fatal(err)
	}

	if _, err := in.Call(t.Context(), "mutate", micropython.FrozenSet(micropython.Int(1))); err == nil {
		t.Error("a frozenset accepted add()")
	} else if !strings.Contains(err.Error(), "AttributeError") {
		t.Errorf("mutating a frozenset: %v, want AttributeError", err)
	}

	// A set is the mutable one, so the same call succeeds.
	if _, err := in.Call(t.Context(), "mutate", micropython.Set(micropython.Int(1))); err != nil {
		t.Errorf("a set refused add(): %v", err)
	}
}
func TestCompositeDictKeys(t *testing.T) {
	for _, expr := range []string{
		"{(1, 2): 'x'}",
		"{frozenset({1}): 'y'}",
		"{b'k': 'z'}",
		"{(1, 2): 'x', 'plain': 'v'}",
	} {
		t.Run(expr, func(t *testing.T) {
			in, err := micropython.NewInstance(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()

			got, err := in.Eval(t.Context(), expr)
			if err != nil {
				t.Fatalf("%s: %v", expr, err)
			}
			if _, ok := got.(map[any]any); !ok {
				t.Errorf("%s = %#v (%T), want map[any]any", expr, got, got)
			}
			if in.Err() != nil {
				t.Errorf("instance died: %v", in.Err())
			}
		})
	}
}
