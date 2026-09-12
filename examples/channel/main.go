// Back a host function with a Go channel. Each call to emit() drains whatever
// is queued and returns it, so the guest pulls work from Go rather than Go
// pushing work into the guest.
package main

import (
	"context"
	"fmt"
	"log"

	micropython "github.com/gregfurman/micropython-go"
)

// row is an ordinary Go struct. Structs cross through encoding/json, so the
// guest sees a dict keyed by the json tags.
type row struct {
	ID    string  `json:"id"`
	Value float64 `json:"value"`
}

const src = `
def drain():
    seen = 0
    total = 0.0
    for r in emit():
        seen += 1
        total += r['value']
    return {'seen': seen, 'total': total}
`

func main() {
	ctx := context.Background()

	// The closure captures the channel, so it is Go state the guest cannot
	// reach except through this one function.
	queue := make(chan row, 8)

	in, err := micropython.NewInstance(ctx,
		micropython.WithHostFunc("emit", func(context.Context, []micropython.Value) (micropython.Value, error) {
			var batch []micropython.Value

			for {
				select {
				case r := <-queue:
					batch = append(batch, micropython.Of(r))
				default:
					// Nothing left right now: return what we have rather than
					// blocking, which would stall the interpreter.
					return micropython.List(batch...), nil
				}
			}
		}),
		micropython.WithSource(src),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	for _, batch := range [][]row{
		{{ID: "a", Value: 1}, {ID: "b", Value: 2}, {ID: "c", Value: 3}},
		{}, // Nothing queued: emit() returns an empty list.
		{{ID: "d", Value: 4}, {ID: "e", Value: 5}},
	} {
		for _, r := range batch {
			queue <- r
		}

		got, err := in.Call(ctx, "drain")
		if err != nil {
			log.Fatal(err)
		}

		fmt.Println(got.Export())
	}
}
