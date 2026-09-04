// Choose the Python type an argument arrives as, and read a result back
// without guessing what it is.
package main

import (
	"context"
	"fmt"
	"log"
	"sort"

	micropython "github.com/gregfurman/micropython-go"
)

const src = `
def kind(v):
    return type(v).__name__

def shipment():
    return {"id": "s-1", "weights": (1.5, 2.5), "tags": {"cold"}}
`

func main() {
	ctx := context.Background()

	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	if err := in.Exec(ctx, src); err != nil {
		log.Fatal(err)
	}

	// Of converts Go data you already have. Where Go has one type for two
	// Python ones, it picks: a slice is always a list.
	for _, v := range []any{
		int64(1),
		"hello",
		[]byte{1, 2},
		[]any{1, 2},
		map[string]any{"a": 1},
		struct {
			A int `json:"a"`
		}{1},
	} {
		got, err := in.Call(ctx, "kind", micropython.Of(v))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Of(%#v) -> %v\n", v, got)
	}

	// The builders say which Python type they mean.
	for _, v := range []micropython.Value{
		micropython.List(micropython.Int(1)),
		micropython.Tuple(micropython.Int(1)),
		micropython.Set(micropython.Int(1)),
		micropython.FrozenSet(micropython.Int(1)),
		micropython.Dict(micropython.Item{Key: micropython.Str("a"), Val: micropython.Int(1)}),
		micropython.None(),
	} {
		got, err := in.Call(ctx, "kind", v)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("built ->", got)
	}

	// Coming back, Export flattens to ordinary Go collections and the As
	// methods keep the distinction.
	got, err := in.Call(ctx, "shipment")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("export: %v\n", got.Export())

	entries, err := got.AsDict()
	if err != nil {
		log.Fatal(err)
	}

	// A dict arrives in the guest's own order, so sort before printing.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Key.String() < entries[j].Key.String()
	})
	for _, e := range entries {
		fmt.Printf("  %v: %v (%s)\n", e.Key, e.Val, e.Val.Type())
	}
}
