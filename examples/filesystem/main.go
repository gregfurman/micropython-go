// Give the guest files to read. Without WithFS there is no filesystem at all:
// import reaches built-in modules only and open() raises OSError.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"

	micropython "github.com/gregfurman/micropython-go"
)

//go:embed testdata
var bundle embed.FS

func main() {
	ctx := context.Background()

	// An fs.FS is mounted at the guest's root, so open() and import both reach
	// it. embed.FS has no write methods, which is what makes this read-only.
	in, err := micropython.NewInstance(ctx, micropython.WithFS(bundle))
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	// Modules on sys.path are imported from the mount like any other.
	if err := in.Exec(ctx, `
import sys
sys.path.append('/testdata')
import config
`); err != nil {
		log.Fatal(err)
	}

	greeting, err := in.Eval(ctx, `config.TEMPLATE % 'world'`)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("imported:", greeting.Export())

	// open() reads the same mount.
	motd, err := in.Eval(ctx, `open('/testdata/motd.txt').read().strip()`)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("read:", motd.Export())

	// Writing needs a backend implementing OpenFileFS, which embed.FS does not,
	// so the guest is told the filesystem is read-only. Errno 30 is EROFS;
	// MicroPython has no name for it, so it arrives as the bare number.
	var exc *micropython.PythonError
	if err := in.Exec(ctx, `open('/testdata/motd.txt', 'w')`); !errors.As(err, &exc) {
		log.Fatalf("expected a Python error, got %v", err)
	}

	fmt.Printf("write refused: %s %s\n", exc.Type(), exc.Message())
}
