// Expose Go functions and values as an importable Python package.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"

	micropython "github.com/gregfurman/micropython-go"
)

func main() {
	ctx := context.Background()

	// Nested Package calls become importable subpackages.
	host := micropython.Package("host",
		micropython.Attribute("service", micropython.Str("orders")),
		micropython.Package("text",
			micropython.Function("upper", upper),
		),
	)

	in, err := micropython.NewInstance(ctx,
		micropython.WithStdout(os.Stdout),
		micropython.WithPackage(host),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	err = in.Exec(ctx, `
import host
from host import text

print(host.service)
print(text.upper("hello"))
`)
	if err != nil {
		log.Fatal(err)
	}
}

func upper(_ context.Context, args []micropython.Value) (micropython.Value, error) {
	if len(args) != 1 {
		return micropython.Value{}, errors.New("upper(s)")
	}

	s, err := args[0].AsString()
	if err != nil {
		return micropython.Value{}, err
	}

	return micropython.Str(strings.ToUpper(s)), nil
}
