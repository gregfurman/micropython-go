// Pull values from a Python generator one at a time, without materialising
// the whole sequence.
package main

import (
	"context"
	"fmt"
	"log"

	micropython "github.com/gregfurman/micropython-go"
)

func main() {
	ctx := context.Background()

	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	err = in.Exec(ctx, `
def squares(n):
    for i in range(1, n + 1):
        yield i * i
`)
	if err != nil {
		log.Fatal(err)
	}

	got, err := in.Call(ctx, "squares", int64(5))
	if err != nil {
		log.Fatal(err)
	}

	// A generator has no Go equivalent, so it crosses as a handle.
	// AsIterator binds it back to the interpreter that produced it.
	it, err := in.AsIterator(got)
	if err != nil {
		log.Fatal(err)
	}

	// Iter yields one value per resumption, so stopping early leaves the rest
	// unevaluated.
	for v, err := range it.Iter(ctx) {
		if err != nil {
			log.Fatal(err)
		}

		n, err := v.AsInt()
		if err != nil {
			log.Fatal(err)
		}
		if n > 9 {
			fmt.Println("stopping at", n)
			break
		}
		fmt.Println("square:", n)
	}
}
