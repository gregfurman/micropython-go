// Let Python call back into Go, and control which exception the guest sees
// when the Go side fails.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	micropython "github.com/gregfurman/micropython-go"
)

var rates = map[string]float64{"EUR": 1.09, "GBP": 1.27}

func main() {
	ctx := context.Background()

	in, err := micropython.NewInstance(ctx, micropython.WithStdout(os.Stdout))
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	if err := in.DefineFunction(ctx, "usd", usd); err != nil {
		log.Fatal(err)
	}
	if err := in.DefineFunction(ctx, "fetch", fetch); err != nil {
		log.Fatal(err)
	}

	err = in.Exec(ctx, `
for code in ("EUR", "GBP", "JPY"):
    try:
        print(code, round(usd(code, 100), 2))
    except KeyError as e:
        print(code, "no rate:", e)

try:
    fetch()
except HostError as e:
    print("host failed:", e)
`)
	if err != nil {
		log.Fatal(err)
	}

	// The same failure reaches Go as an ordinary error.
	var exc *micropython.PythonError
	if _, err := in.Eval(ctx, "fetch()"); errors.As(err, &exc) {
		fmt.Printf("go sees: %s / %s\n", exc.Type(), exc.Message())
	}
}

// usd converts an amount using a rate table held in Go.
func usd(_ context.Context, args []micropython.Value) (micropython.Value, error) {
	if len(args) != 2 {
		return micropython.Value{}, errors.New("usd(code, amount)")
	}

	code, err := args[0].AsString()
	if err != nil {
		return micropython.Value{}, err
	}

	rate, ok := rates[code]
	if !ok {
		// Raise names the Python class the guest catches.
		return micropython.Value{}, micropython.Raise("KeyError", code)
	}

	amount, err := args[1].AsInt()
	if err != nil {
		return micropython.Value{}, err
	}

	return micropython.Float(rate * float64(amount)), nil
}

// fetch fails for a reason with no Python equivalent, so the guest sees
// HostError, a class this port adds under RuntimeError.
func fetch(context.Context, []micropython.Value) (micropython.Value, error) {
	return micropython.Value{}, errors.New("connection refused")
}
