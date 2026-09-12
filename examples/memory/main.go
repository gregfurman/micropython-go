// Size the Python heap to what a script allocates. Too small and the guest
// raises MemoryError, which is catchable and leaves the interpreter usable.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	micropython "github.com/gregfurman/micropython-go"
)

// Allocating a list of this many small strings needs more than the small heap
// below and fits comfortably in the larger one.
const src = `
def build(n):
    items = ['item %d' % i for i in range(n)]
    return len(items)
`

func main() {
	ctx := context.Background()

	for _, heap := range []int{32 * 1024, 512 * 1024} {
		in, err := micropython.NewInstance(ctx,
			micropython.WithSource(src),
			micropython.WithHeapSize(heap),
		)
		if err != nil {
			log.Fatal(err)
		}

		got, err := in.Call(ctx, "build", int64(2000))
		switch err {
		case nil:
			// The list stays in the guest: only its length crosses, so this
			// measures the Python heap rather than the transfer region.
			fmt.Printf("%3dKiB heap: built %v items\n", heap/1024, got.Export())

		default:
			// A guest that runs out of heap is an ordinary Go error, and the
			// interpreter still works afterwards.
			var exc *micropython.PythonError
			if !errors.As(err, &exc) {
				log.Fatal(err)
			}

			fmt.Printf("%3dKiB heap: %s\n", heap/1024, exc.Type())

			alive, err := in.Eval(ctx, "1 + 1")
			if err != nil {
				log.Fatal(err)
			}

			fmt.Println("             still usable:", alive.Export())
		}

		in.Close()
	}
}
