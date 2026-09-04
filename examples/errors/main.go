// Handle a guest that raises, and stop one that will not finish.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	micropython "github.com/gregfurman/micropython-go"
)

const src = `
def lookup(key):
    return {"a": 1}[key]

def spin():
    while True:
        pass
`

func main() {
	ctx := context.Background()

	p, err := micropython.CompileSource(ctx, src)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	// A raise comes back as an ordinary Go error carrying the exception, so
	// callers branch on the class rather than on the message.
	var exc *micropython.PythonError
	if _, err := p.Call(ctx, "lookup", "missing"); errors.As(err, &exc) {
		fmt.Println("raised:", exc.Type(), "/", exc.Message())
	}

	// The interpreter is unharmed: the failure was the guest's.
	got, err := p.Call(ctx, "lookup", "a")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("recovered:", got)

	// A call stops when its context does. There is no scheduler inside the
	// module, so this is what bounds a runaway loop.
	deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	if _, err := p.Call(deadline, "spin"); errors.Is(err, context.DeadlineExceeded) {
		fmt.Println("stopped at the deadline")
	}

	// An Instance can also be interrupted from another goroutine.
	in, err := p.Instance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	time.AfterFunc(100*time.Millisecond, func() { in.Cancel() })

	if _, err := in.Call(ctx, "spin"); errors.As(err, &exc) {
		fmt.Println("cancelled:", exc.Type())
	}
}
