// extmodule installs a Go-provided Python package and imports it from guest
// code like any other module.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"

	"github.com/gregfurman/micropython-go"
)

func main() {
	ctx := context.Background()

	// The package tree is built in Go, but its modules and attributes are
	// ordinary MicroPython objects. Go callbacks are reached through the
	// package's ID-bound callable proxies.
	host := micropython.Package("host",
		micropython.Attribute("service_name", micropython.Str("package demo")),
		micropython.Package("strings",
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

	if err := in.Exec(ctx, `
import host
from host import strings

print(host.service_name)
print(strings.upper("hello from Python"))
print(type(host))
`); err != nil {
		log.Fatal(err)
	}
}

func upper(_ context.Context, args []micropython.Value) (micropython.Value, error) {
	if len(args) != 1 {
		return micropython.Value{}, errors.New("upper expects one string")
	}
	s, err := args[0].AsString()
	if err != nil {
		return micropython.Value{}, err
	}
	return micropython.Str(strings.ToUpper(s)), nil
}
