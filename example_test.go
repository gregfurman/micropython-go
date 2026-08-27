package micropython_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	micropython "github.com/gregfurman/micropython-go"
)

// Compile once, call many times. A Program holds the interpreter as it stood
// after the source ran, so every call starts from that point rather than
// re-running the module.
func Example() {
	ctx := context.Background()

	p, err := micropython.Compile(ctx, `
def score(row):
    total = row["a"] * 2 + row["b"]
    return {"id": row["id"], "score": total, "ok": total > 10}
`)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	row := micropython.Of(map[string]any{"id": "r-1", "a": 4, "b": 5})

	got, err := p.Call(ctx, "score", row)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(got)

	// Output:
	// map[id:r-1 ok:true score:13]
}

// Arguments are Python values, built rather than guessed at. A Go slice could
// be a list, a tuple or a set, so the call says which.
func ExampleValue() {
	ctx := context.Background()

	p, err := micropython.Compile(ctx, "def kind(v):\n    return type(v).__name__\n")
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	for _, v := range []micropython.Value{
		micropython.Int(1),
		micropython.Str("hello"),
		micropython.Bytes([]byte{1, 2}),
		micropython.None(),
		micropython.List(micropython.Int(1), micropython.Int(2)),
		micropython.Tuple(micropython.Int(1), micropython.Int(2)),
		micropython.Set(micropython.Int(1)),
		micropython.Dict(micropython.Item{Key: micropython.Str("a"), Val: micropython.Int(1)}),
	} {
		got, err := p.Call(ctx, "kind", v)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(got)
	}

	// Output:
	// int
	// str
	// bytes
	// NoneType
	// list
	// tuple
	// set
	// dict
}

// Of converts Go data you already have. It is the open end of the boundary:
// convenient, and a guess where Go has one type for two Python ones -- a slice
// becomes a list, never a tuple.
func ExampleOf() {
	ctx := context.Background()

	p, err := micropython.Compile(ctx, "def kind(v):\n    return type(v).__name__\n")
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	for _, v := range []any{
		[]any{1, 2},
		map[string]any{"a": 1},
		struct {
			A int `json:"a"`
		}{1},
		[]string{"x"},
	} {
		got, err := p.Call(ctx, "kind", micropython.Of(v))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(got)
	}

	// A value with no Python equivalent reports when the call uses it, so Of
	// can be written inline.
	if _, err := p.Call(ctx, "kind", micropython.Of(func() {})); err != nil {
		fmt.Println("error:", err)
	}

	// Output:
	// list
	// dict
	// dict
	// list
	// error: micropython: cannot pass func() to Python: json: unsupported type: func()
}

// Globals reach the source without being spliced into its text, so nothing has
// to be quoted or escaped.
func ExampleWithGlobals() {
	ctx := context.Background()

	p, err := micropython.Compile(ctx, `
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

// A guest that raises comes back as an ordinary Go error carrying the
// exception's type and message.
func ExampleProgram_Call_error() {
	ctx := context.Background()

	p, err := micropython.Compile(ctx, `
def lookup(key):
    return {"a": 1}[key]
`)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	// The error carries the exception, so a caller can branch on which one it
	// was rather than reading the message.
	var exc *micropython.PythonError
	if _, err := p.Call(ctx, "lookup", micropython.Str("missing")); errors.As(err, &exc) {
		fmt.Println(exc.Type(), "/", exc.Message())
	}

	// The Program is unharmed: the failure was the guest's, not the
	// interpreter's.
	got, err := p.Call(ctx, "lookup", micropython.Str("a"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(got)

	// Output:
	// KeyError / missing
	// 1
}

// A host can hand the guest an exception to raise, named so that the guest
// catches it as itself.
func ExampleException() {
	ctx := context.Background()

	p, err := micropython.Compile(ctx, `
def run():
    try:
        raise BAD
    except ValueError as e:
        return "caught " + str(e)
`, micropython.WithGlobals(micropython.Globals{
		"BAD": micropython.Exception("ValueError", "bad input"),
	}))
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	got, err := p.Call(ctx, "run")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(got)

	// Output:
	// caught bad input
}

// Calls run in parallel. A Program keeps a pool of interpreters restored from
// the same snapshot, so callers do not queue behind each other, and no call
// can see what another one did.
func ExampleProgram_Call_concurrent() {
	ctx := context.Background()

	p, err := micropython.Compile(ctx, `
_seen = 0

def visit(n):
    global _seen
    _seen += 1
    return [n * 2, _seen]
`)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	var (
		mu      sync.Mutex
		doubled []int
		wg      sync.WaitGroup
	)
	for i := range 8 {
		wg.Go(func() {
			got, err := p.Call(ctx, "visit", micropython.Int(int64(i)))
			if err != nil {
				return
			}
			out := got.([]any)
			mu.Lock()
			defer mu.Unlock()
			// out[1] is always 1: each call starts from the snapshot, so the
			// increment another call made is not there.
			doubled = append(doubled, int(out[0].(int64))+int(out[1].(int64))-1)
		})
	}
	wg.Wait()

	sort.Ints(doubled)
	fmt.Println(doubled)

	// Output:
	// [0 2 4 6 8 10 12 14]
}

// A runaway guest stops when the context does. There is no scheduler inside
// the module, so this is what bounds a call.
func ExampleProgram_Call_cancellation() {
	p, err := micropython.Compile(context.Background(), `
def spin():
    while True:
        pass

def add(a, b):
    return a + b
`)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if _, err := p.Call(ctx, "spin"); errors.Is(err, context.DeadlineExceeded) {
		fmt.Println("stopped at the deadline")
	}

	// And the Program is usable afterwards.
	got, err := p.Call(context.Background(), "add", micropython.Int(20), micropython.Int(22))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(got)

	// Output:
	// stopped at the deadline
	// 42
}

// An Instance is one interpreter that keeps what it is told, for a session
// rather than a handler. Unlike a Program, state carries between calls.
func ExampleInstance() {
	ctx := context.Background()

	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	if _, err := in.Exec(ctx, `
total = 0

def add(n):
    global total
    total += n
    return total
`); err != nil {
		log.Fatal(err)
	}

	var running []any
	for _, n := range []int64{1, 2, 3} {
		got, err := in.Call(ctx, "add", micropython.Int(n))
		if err != nil {
			log.Fatal(err)
		}
		running = append(running, got)
	}
	fmt.Println(running...)

	// Eval reads one expression back.
	got, err := in.Eval(ctx, "total * 10")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(got)

	// Output:
	// 1 3 6
	// 60
}

// Exec returns whatever the source printed, for code written to print rather
// than return.
func ExampleInstance_Exec() {
	ctx := context.Background()

	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	out, err := in.Exec(ctx, "for i in range(3):\n    print('line', i)\n")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(out)

	// Output:
	// line 0
	// line 1
	// line 2
}

// DefineFunction lets Python call back into Go. The binding is part of the
// interpreter's state, so it stays available to everything that runs after it.
func ExampleInstance_DefineFunction() {
	ctx := context.Background()

	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	rates := map[string]float64{"EUR": 1.09, "GBP": 1.27}

	// Arguments arrive as native Go values; the result is converted back.
	err = in.DefineFunction(ctx, "usd", func(args []any) (any, error) {
		code, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("usd: want a currency code, got %T", args[0])
		}
		rate, ok := rates[code]
		if !ok {
			return nil, micropython.Raise("KeyError", code)
		}
		return rate * float64(args[1].(int64)), nil
	})
	if err != nil {
		log.Fatal(err)
	}

	out, err := in.Exec(ctx, `
for code in ("EUR", "GBP", "JPY"):
    try:
        print(code, round(usd(code, 100), 2))
    except KeyError as e:
        print(code, "no rate for", e)
`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(out)

	// Output:
	// EUR 109.0
	// GBP 127.0
	// JPY no rate for JPY
}

// A host function that fails for a reason with no Python equivalent raises
// HostError, so guest code can catch host-boundary failures on their own
// without also catching the interpreter's errors.
func ExampleInstance_DefineFunction_error() {
	ctx := context.Background()

	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	if err := in.DefineFunction(ctx, "fetch", func([]any) (any, error) {
		return nil, errors.New("connection refused")
	}); err != nil {
		log.Fatal(err)
	}

	// In Python, as HostError.
	out, err := in.Exec(ctx, `
try:
    fetch()
except HostError as e:
    print("guest caught:", e)
`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print(out)

	// In Go, as an ordinary error carrying the same text.
	var exc *micropython.PythonError
	if _, err := in.Eval(ctx, "fetch()"); errors.As(err, &exc) {
		fmt.Printf("host saw: %s / %s\n", exc.Type(), exc.Message())
	}

	// Output:
	// guest caught: connection refused
	// host saw: HostError / connection refused
}
