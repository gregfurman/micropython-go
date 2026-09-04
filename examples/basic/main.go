// Run a script in a stateful interpreter, then read values back out.
package main

import (
	"context"
	"fmt"
	"log"

	micropython "github.com/gregfurman/micropython-go"
)

const src = `
readings = []

def record(celsius):
    readings.append(celsius)
    return len(readings)

def average():
    return sum(readings) / len(readings)
`

func main() {
	ctx := context.Background()

	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	// Exec runs statements. Everything it defines stays available.
	if err := in.Exec(ctx, src); err != nil {
		log.Fatal(err)
	}

	for _, celsius := range []int64{18, 21, 24} {
		got, err := in.Call(ctx, "record", celsius)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("readings:", got)
	}

	// Eval runs a single expression.
	avg, err := in.Eval(ctx, "round(average(), 1)")
	if err != nil {
		log.Fatal(err)
	}

	// As* converts to a specific Go type and reports a mismatch.
	f, err := avg.AsFloat()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("average: %.1fC\n", f)

	// Get reads a global by name without compiling an expression.
	all, err := in.Get(ctx, "readings")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("exported:", all.Export())
}
