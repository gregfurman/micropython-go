package micropython_test

import (
	"context"
	"errors"
	"fmt"
	"log"

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

	if err := in.Exec(ctx, "def scale(xs, by):\n    return [x * by for x in xs]\n"); err != nil {
		log.Fatal(err)
	}

	got, err := in.Call(ctx, "scale", []int64{1, 2, 3}, 10)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(got.Export())

	// Output:
	// [10 20 30]
}

// A Go slice could be a list, a tuple or a set, so the builders say which.
func ExampleValue() {
	ctx := context.Background()

	p, err := micropython.CompileSource(ctx, "def kind(v):\n    return type(v).__name__\n")
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	for _, v := range []any{
		[]any{1, 2},
		micropython.Tuple(micropython.Int(1), micropython.Int(2)),
	} {
		got, err := p.Call(ctx, "kind", v)
		if err != nil {
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

	p, err := micropython.CompileSource(ctx, `
def describe():
    return "%s allows %d retries" % (NAME, LIMITS["retries"])
`, micropython.WithGlobals(micropython.Globals{
		"NAME":   micropython.Str("service"),
		"LIMITS": micropython.Dict(micropython.Item{Key: micropython.Str("retries"), Val: micropython.Int(3)}),
	}))
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	got, err := p.Call(ctx, "describe")
	if err != nil {
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

	rates := map[string]float64{"EUR": 1.09}

	err = in.DefineFunction(ctx, "usd", func(_ context.Context, args []micropython.Value) (micropython.Value, error) {
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

	p, err := micropython.CompileSource(ctx, "def lookup(key):\n    return {\"a\": 1}[key]\n")
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	var exc *micropython.PythonError
	if _, err := p.Call(ctx, "lookup", "missing"); errors.As(err, &exc) {
		fmt.Println(exc.Type(), "/", exc.Message())
	}

	got, err := p.Call(ctx, "lookup", "a")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(got)

	// Output:
	// KeyError / missing
	// 1
}
