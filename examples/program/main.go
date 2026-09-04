// Compile a script once and serve concurrent calls from a pool of
// interpreters, each rewound to the compiled state before it is reused.
package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"

	micropython "github.com/gregfurman/micropython-go"
)

const src = `
calls = 0

def score(row):
    global calls
    calls += 1
    return {"id": row["id"], "total": row["a"] * 2 + row["b"], "calls": calls}
`

func main() {
	ctx := context.Background()

	p, err := micropython.CompileSource(ctx, src, micropython.WithPoolSize(4))
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	var (
		mu      sync.Mutex
		results []string
		wg      sync.WaitGroup
	)
	for i := range 4 {
		wg.Go(func() {
			row := map[string]any{"id": fmt.Sprintf("r-%d", i), "a": i, "b": 1}

			got, err := p.Call(ctx, "score", row)
			if err != nil {
				log.Fatal(err)
			}

			out := got.Export().(map[string]any)
			mu.Lock()
			defer mu.Unlock()
			// calls is always 1: every call starts from the compiled state, so
			// the increments other calls made are not there.
			results = append(results, fmt.Sprintf("%v total=%v calls=%v",
				out["id"], out["total"], out["calls"]))
		})
	}
	wg.Wait()

	sort.Strings(results)
	for _, r := range results {
		fmt.Println(r)
	}

	// Instance detaches one interpreter from the pool. It starts from the same
	// compiled state but keeps what it is told, so calls accumulate.
	in, err := p.Instance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	for range 3 {
		if _, err := in.Call(ctx, "score", map[string]any{"id": "x", "a": 1, "b": 1}); err != nil {
			log.Fatal(err)
		}
	}

	calls, err := in.Eval(ctx, "calls")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("detached instance calls:", calls)
}
