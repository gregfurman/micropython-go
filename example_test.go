package micropython_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	micropython "github.com/gregfurman/micropython-go"
)

// The README's quick start: one interpreter, a script, and a call.
func Example() {
	ctx := context.Background()

	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	if err := in.Exec(ctx, "double = lambda x: x * 2"); err != nil {
		log.Fatal(err)
	}

	got, err := in.Call(ctx, "double", 10)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(got.Export())

	// Output:
	// 20
}

func ExampleValue_AsInt() {
	ctx := context.Background()

	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	if err := in.Set(ctx, "numbers", []int{1, 2, 3}); err != nil {
		log.Fatal(err)
	}

	got, err := in.Eval(ctx, "sum(numbers)")
	if err != nil {
		log.Fatal(err)
	}

	n, err := got.AsInt()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(n)
	// Output: 6
}

func ExampleNewInstance_startup() {
	ctx := context.Background()

	in, err := micropython.NewInstance(ctx,
		micropython.WithGlobals(micropython.Globals{"LOCATION": "New York"}),
		micropython.WithSource(`greeting = "Hello from " + LOCATION`),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	got, err := in.Get(ctx, "greeting")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(got.Export())
	// Output: Hello from New York
}

func ExampleProgram_Run_basic() {
	ctx := context.Background()

	p, err := micropython.NewProgram(ctx,
		micropython.WithSource("def double(x): return x * 2"),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	err = p.Run(ctx, func(in *micropython.BorrowedInstance) error {
		got, err := in.Call(ctx, "double", 10)
		if err != nil {
			return err
		}

		fmt.Println(got.Export())

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	// Output: 20
}

func TestREADMESandbox(t *testing.T) {
	ctx := t.Context()

	dir := t.TempDir()
	if err := os.WriteFile(dir+"/message.txt", []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	in, err := micropython.NewInstance(ctx,
		micropython.WithStdout(os.Stdout),
		micropython.WithEnv("ENV", "dev"),
		micropython.WithFS(root.FS()),
		micropython.WithTCPAccess("127.0.0.1", 8000),
		micropython.WithTCPAccess(micropython.AnyAddress, 443),
		micropython.WithDNSResolver(net.DefaultResolver),
		micropython.WithHeapSize(256*1024),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	if err := in.Exec(ctx, `
import os
assert os.getenv("ENV") == "dev"
with open("/message.txt") as f:
    assert f.read() == "hello"
try:
    with open("/message.txt", "w"):
        pass
except OSError:
    pass
else:
    raise AssertionError("filesystem must be read-only")
`); err != nil {
		t.Fatal(err)
	}
}

func ExampleNewInstance_readOnlyFiles() {
	ctx := context.Background()
	files := fstest.MapFS{
		"message.txt": &fstest.MapFile{Data: []byte("hello")},
	}

	var output bytes.Buffer

	in, err := micropython.NewInstance(ctx,
		micropython.WithFS(micropython.ReadOnly(files)),
		micropython.WithEnv("STAGE", "sandbox"),
		micropython.WithStdout(&output),
		micropython.WithHeapSize(256*1024),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	if err := in.Exec(ctx, `
import os
with open('message.txt') as f:
    print(os.getenv('STAGE'), f.read())
`); err != nil {
		log.Fatal(err)
	}

	fmt.Print(output.String())
	// Output: sandbox hello
}

func ExampleNewInstance_sandbox() {
	ctx := context.Background()

	deadline, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	in, err := micropython.NewInstance(deadline,
		micropython.WithEnv("APP_MODE", "agent"),
		micropython.WithHeapSize(256*1024),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	result, err := in.Eval(deadline, "sum(range(101))")
	if err != nil {
		log.Fatal(err)
	}

	n, err := result.AsInt()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(n)
	// Output: 5050
}

func ExampleProgram_Run() {
	ctx := context.Background()

	p, err := micropython.NewProgram(ctx, micropython.WithSource(`
def score(row):
    return {"id": row["id"], "total": row["a"] * 2 + row["b"]}
`))
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	var out map[string]any

	err = p.Run(ctx, func(in *micropython.BorrowedInstance) error {
		got, err := in.Call(ctx, "score", map[string]any{"id": "r-1", "a": 4, "b": 5})
		if err != nil {
			return err
		}

		out = got.Export().(map[string]any)

		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(out["id"], out["total"])
	// Output: r-1 13
}

// A Go slice could be a list, a tuple or a set, so the builders say which.
func ExampleValue() {
	ctx := context.Background()

	p, err := micropython.NewProgram(ctx, micropython.WithSource("def kind(v):\n    return type(v).__name__\n"))
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	for _, v := range []any{
		[]any{1, 2},
		micropython.Tuple(micropython.Int(1), micropython.Int(2)),
	} {
		var got micropython.Value

		if err := p.Run(ctx, func(in *micropython.BorrowedInstance) error {
			v, err := in.Call(ctx, "kind", v)
			got = v

			return err
		}); err != nil {
			log.Fatal(err)
		}

		fmt.Println(got)
	}

	// Output:
	// list
	// tuple
}

// Globals reach the source without being spliced into its text, so nothing has
// to be quoted or escaped.
func ExampleWithGlobals() {
	ctx := context.Background()

	p, err := micropython.NewProgram(ctx, micropython.WithSource(`
def describe():
    return "%s allows %d retries" % (NAME, LIMITS["retries"])
`), micropython.WithGlobals(micropython.Globals{
		"NAME":   micropython.Str("service"),
		"LIMITS": micropython.Dict(micropython.Item{Key: micropython.Str("retries"), Val: micropython.Int(3)}),
	}))
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	var got micropython.Value

	if err := p.Run(ctx, func(in *micropython.BorrowedInstance) error {
		v, err := in.Call(ctx, "describe")
		got = v

		return err
	}); err != nil {
		log.Fatal(err)
	}

	fmt.Println(got)

	// Output:
	// service allows 3 retries
}

// Raise picks the exception class the guest catches.
func ExampleInstance_DefineFunction() {
	ctx := context.Background()

	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	rates := map[string]float64{"EUR": 1.09, "GBP": 1.27} // Example rates.

	err = in.DefineFunction(ctx, "usd", func(_ context.Context, args []micropython.Value) (micropython.Value, error) {
		if len(args) != 1 {
			return micropython.Value{}, micropython.Raise("TypeError", "usd expects one currency code")
		}

		code, err := args[0].AsString()
		if err != nil {
			return micropython.Value{}, err
		}

		rate, ok := rates[code]
		if !ok {
			return micropython.Value{}, micropython.Raise("KeyError", code)
		}

		return micropython.Float(rate), nil
	})
	if err != nil {
		log.Fatal(err)
	}

	got, err := in.Eval(ctx, `usd("EUR")`)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(got)

	var exc *micropython.PythonError
	if _, err := in.Eval(ctx, `usd("JPY")`); errors.As(err, &exc) {
		fmt.Println(exc.Type(), "/", exc.Message())
	}

	// Output:
	// 1.09
	// KeyError / JPY
}

// A guest that raises comes back as an ordinary Go error and leaves the
// interpreter usable.
func ExamplePythonError() {
	ctx := context.Background()

	p, err := micropython.NewProgram(ctx, micropython.WithSource("def lookup(key):\n    return {\"a\": 1}[key]\n"))
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	err = p.Run(ctx, func(in *micropython.BorrowedInstance) error {
		_, err := in.Call(ctx, "lookup", "missing")
		return err
	})
	if exc, ok := errors.AsType[*micropython.PythonError](err); ok {
		fmt.Println(exc.Type(), "/", exc.Message())
	}

	var got micropython.Value

	if err := p.Run(ctx, func(in *micropython.BorrowedInstance) error {
		v, err := in.Call(ctx, "lookup", "a")
		got = v

		return err
	}); err != nil {
		log.Fatal(err)
	}

	fmt.Println(got)

	// Output:
	// KeyError / missing
	// 1
}

func ExampleOption_from_host() {
	ctx := context.Background()

	src := `def is_louder(greeting): return greeting.isupper()`

	instance, err := micropython.NewInstance(ctx,
		micropython.WithSource(src),
		micropython.WithGlobals(micropython.Globals{
			"LOCATION":      micropython.Str("New York"),
			"SERVICE_COUNT": micropython.Int(42),
		}),
		micropython.WithHostFunc("louder",
			func(ctx context.Context, args []micropython.Value) (micropython.Value, error) {
				greet, err := args[0].AsString()
				if err != nil {
					return micropython.Value{}, err
				}

				return micropython.Str(strings.ToUpper(greet)), nil
			}),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer instance.Close()

	result, err := instance.Eval(ctx, `(
    f"{louder('hello from ' + LOCATION)} "
    f"({SERVICE_COUNT} services), "
    f"loud: {is_louder(louder('hi'))}"
)`)
	if err != nil {
		log.Fatal(err)
	}

	out, err := result.AsString()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(out)

	// Output:
	// HELLO FROM NEW YORK (42 services), loud: True
}

func ExampleOption_sandbox() {
	ctx := context.Background()

	root, _ := os.OpenRoot("./testdata/filesystem")

	instance, err := micropython.NewInstance(ctx,
		micropython.WithStdout(os.Stdout),                      // redirect all print() to host's STDOUT
		micropython.WithEnv("ENV", "dev"),                      // the only variable os.getenv can see
		micropython.WithFS(root.FS()),                          // provide read-only access to in-memory FS
		micropython.WithTCPAccess("127.0.0.1", 8000),           // this address and port, outbound
		micropython.WithTCPAccess(micropython.AnyAddress, 443), // any address, but only port 443
		micropython.WithDNSResolver(net.DefaultResolver),       // needed for DNS resolution
	)
	if err != nil {
		log.Fatal(err)
	}
	defer instance.Close()

	script := `
import os

print("env:", os.getenv("ENV"))
print("mounted:", os.listdir("/"))

with open("hello.txt") as f:
    print("file:", f.read().strip())
`

	if err := instance.Exec(ctx, script); err != nil {
		log.Fatal(err)
	}

	// Output:
	// env: dev
	// mounted: ['hello.txt']
	// file: hello from the sandbox
}
