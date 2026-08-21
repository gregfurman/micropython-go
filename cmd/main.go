// Command micropython runs a Python expression, with Ctrl-C interrupting the
// guest rather than killing the process.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	micropython "github.com/gregfurman/micropython-wasi"
)

const guestBudget = 30 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

const src = `
def louder(idx, v):
  return f"{idx}: {v.upper()}"
`

func run() error {

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	prog, err := micropython.Compile(ctx, src)
	if err != nil {
		return err
	}

	defer prog.Close()

	var wg sync.WaitGroup
	wg.Add(10)

	for i := range 10 {
		go func() {
			defer wg.Done()
			out, err := prog.Call(ctx, "louder", i, "hello")
			if err != nil {
				panic(err)
			}
			fmt.Printf("out: %v\n", out)
		}()
	}

	wg.Wait()

	return nil
}
