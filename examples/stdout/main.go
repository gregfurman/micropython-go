// Read what the guest prints, either collected afterwards or streamed as it
// runs.
package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"sync"

	micropython "github.com/gregfurman/micropython-go"
)

func main() {
	collect()
	stream()
}

// collect buffers the output and reads it once the call is done. Nothing the
// guest prints escapes the interpreter without WithStdout.
func collect() {
	ctx := context.Background()

	var out bytes.Buffer
	in, err := micropython.NewInstance(ctx, micropython.WithStdout(&out))
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	if err := in.Exec(ctx, "def greet(who):\n    print('hello', who)\n"); err != nil {
		log.Fatal(err)
	}

	// One writer serves the whole interpreter, so Call prints there too. It is
	// the only way to see what a Call printed, since Call returns a value.
	if _, err := in.Call(ctx, "greet", "world"); err != nil {
		log.Fatal(err)
	}
	fmt.Print(out.String())
}

// stream reads alongside the script rather than after it. A bytes.Buffer is
// not safe for that; an io.Pipe is.
func stream() {
	ctx := context.Background()

	pr, pw := io.Pipe()
	in, err := micropython.NewInstance(ctx, micropython.WithStdout(pw))
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			fmt.Println("read:", sc.Text())
		}
	}()

	err = in.Exec(ctx, "for i in range(3):\n    print('tick', i)\n")

	// Closing the writer ends the stream, so the reader sees io.EOF.
	pw.Close()
	wg.Wait()

	if err != nil {
		log.Fatal(err)
	}
}
