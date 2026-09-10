// Hold a Python function in Go, call it, and pass it back to Python.
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

	// A lambda has no global name, so it comes back as a handle rather than a
	// value. AsCallable binds the handle to the interpreter that made it.
	got, err := in.Eval(ctx, "lambda x: x * 2")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("callable:", got.IsCallable())

	double, err := in.AsCallable(got)
	if err != nil {
		log.Fatal(err)
	}

	out, err := double.Call(ctx, int64(21))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("double(21):", out)

	// Passing it back hands Python the same function object, so builtins that
	// take a callable work unchanged. map is lazy, so list() drains it.
	lazy, err := in.Call(ctx, "map", double, []int64{1, 2, 3})
	if err != nil {
		log.Fatal(err)
	}

	mapped, err := in.Call(ctx, "list", lazy)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("mapped:", mapped.Export())
}
