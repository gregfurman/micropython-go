# Embedded MicroPython Environment For Go

`micropython-go` is a `cgo`-free embeddable interpreter for [MicroPython](https://github.com/micropython/micropython).

It uses a custom [`WASM`](https://webassembly.org/) build of MicroPython, transpiled to native Go code using [wasm2go](https://github.com/ncruces/wasm2go).

> [!IMPORTANT]
> This project is still experimental and until a tagged (and stable) version is released, both the public API and internal modules are subject to breaking changes.

## Installation

```bash
go get github.com/gregfurman/micropython-go
```

## Quick Start

The following shows an example of creating a MicroPython instance, and using it's builtin `len` function to ascertain the length of a string:

```go
package main

import (
	"context"
	"fmt"
	"log"

	micropython "github.com/gregfurman/micropython-go"
)

func main() {
	ctx := context.Background()

	// Boots an interpreter.
	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	// Close should be called to ensure this cleans-up
	defer in.Close()

	// Call reaches any Python global by name, builtins included. Arguments here are
	// ordinary Go values, lowered to an equivalent Python type on the way in.
	result, err := in.Call(ctx, "len", "abc")
	if err != nil {
		log.Fatal(err)
	}

	// Results come back converted too. Every Python int arrives as an int64.
	fmt.Printf("The length of 'abc' is %d\n", result.(int64))
}

// Output:
// The length of 'abc' is 3
```

## Instances

An `Instance` is one long-running interpreter. State persists between evaluations, and calls are fast because nothing is rewound between them.

It is safe to call from several goroutines, but a single interpreter runs one call at a time, so concurrent calls queue rather than overlap. For parallelism, give each worker its own interpreter with `Clone`, or use a `Program`.

```go
in, _ := micropython.NewInstance(ctx)
defer in.Close()

in.Exec(ctx, "total = 0")
in.Exec(ctx, "total += 5")

val, _ := in.Eval(ctx, "total * 2")
fmt.Println(val) // 10
```

## Programs

A `Program` compiles a script once and serves calls from a pool of interpreters, so `Call` is safe across goroutines.

Compiling boots one interpreter, runs the source, and snapshots its memory. Later instances are restored from that snapshot rather than booted, so each call starts from the post-script state without re-running it. Nothing in the Python heap survives a call; effects that reached outside the guest are not rewound.

```go
p, err := micropython.CompileSource(ctx, `
def score(row):
    total = row["a"] * 2 + row["b"]
    return {"id": row["id"], "score": total, "ok": total > 10}
`)
if err != nil {
	log.Fatal(err)
}
defer p.Close()

got, err := p.Call(ctx, "score", map[string]any{"id": "r-1", "a": 4, "b": 5})
if err != nil {
	log.Fatal(err)
}

fmt.Printf("%#v\n", got)
// map[string]interface {}{"id":"r-1", "ok":true, "score":13}
```

The pool defaults to `runtime.NumCPU()`; set it with `WithPoolSize`.

### Heap size

An interpreter costs its Python heap plus about `250 KiB` of interpreter image, and nothing is shared between them. The heap defaults to `128 KiB`, so a single `Instance` is roughly `0.4 MiB`.

A `Program` also holds a snapshot and builds interpreters as concurrency demands them, up to `WithPoolSize`: about `0.7 MiB` idle, and about `2.3 MiB` after twelve calls have run in parallel.

Restoring a snapshot copies the heap, so smaller heaps also make isolated calls cheaper:

```go
p, err := micropython.CompileSource(ctx, src, micropython.WithHeapSize(256*1024))
```

Too small and the guest raises `MemoryError`, leaving the `Program` usable. Size it to what your script actually allocates.

## Values

Arguments are ordinary Go values, converted recursively. Custom structs go through an `encoding/json` fallback.

| Go Type                               | Python Type                                      |
|---------------------------------------|--------------------------------------------------|
| `nil`                                 | `None`                                           |
| `bool`                                | `bool`                                           |
| all signed and unsigned integer types | `int`                                            |
| `float32`, `float64`                  | `float`                                          |
| `string`                              | `str`                                            |
| `[]byte`                              | `bytes`                                          |
| non-byte slices and arrays            | `list`                                           |
| Go maps                               | `dict`                                           |
| `micropython.Tuple(...)`              | `tuple`                                          |
| `micropython.Set(...)`                | `set`                                            |
| `micropython.FrozenSet(...)`          | `frozenset`                                      |
| `struct{...}`                         | *JSON round-trip to* `dict`                      |
| `micropython.Of(v)`                   | whatever the rules above make of `v`, explicitly |

Where Go's type doesn't preserve the Python distinction you want (e.g a slice could mean `list` or `tuple`) use the builders:

```go
p.Call(ctx, "f", []any{1, 2})                           // list
p.Call(ctx, "f", micropython.Tuple(micropython.Int(1))) // tuple
```

Configuration can be bound as globals instead of spliced into the source:

```go
p, err := micropython.CompileSource(ctx, src, micropython.WithGlobals(micropython.Globals{
    "NAME":   micropython.Str("service"),
    "LIMITS": micropython.Dict(micropython.Item{Key: micropython.Str("retries"), Val: micropython.Int(3)}),
}))
```

## Host functions

`DefineFunction` binds a Go function to a global Python callable. 

Note, that when using `micropython.WithHostFunc`, host functions will be prior to a source script being loaded in. This allows for scripts to reference host functions.

```go
in, _ := micropython.NewInstance(ctx)
defer in.Close()

rates := map[string]float64{"EUR": 1.09, "GBP": 1.27}

in.DefineFunction(ctx, "usd", func(args []any) (any, error) {
    code := args[0].(string)
    rate, ok := rates[code]
    if !ok {
        return nil, micropython.Raise("KeyError", code)
    }
    return rate * float64(args[1].(int64)), nil
})

out, _ := in.Exec(ctx, `print(round(usd("EUR", 100), 2))`)
fmt.Print(out) // 109.0
```

Arguments and return values convert per the table above. A binding is part of interpreter state, so it lasts for the life of the `Instance` and any `Clone` taken afterwards inherits it. In addition, when used with a `Program`, the same host function closure will be shared across instances.

## Errors and cancellation

A guest that raises comes back as an ordinary Go error and leaves the interpreter usable:

```go
var exc *micropython.PythonError
if _, err := p.Call(ctx, "lookup", "missing"); errors.As(err, &exc) {
    fmt.Println(exc.Type())    // KeyError
    fmt.Println(exc.Message()) // missing
    fmt.Println(exc.Raw())     // the traceback as MicroPython printed it
}
```

In the other direction, an error returned from a host function raises at the Python call site as `HostError`, a class this port adds so guest code can single out host-boundary failures. It subclasses `RuntimeError`, so existing handlers still catch it. `micropython.Raise` resolves against the builtin exceptions instead, falling back to `HostError` for unknown names:

```go
return nil, micropython.Raise("KeyError", code) // guest catches KeyError
```

A panic inside a host function is recovered and raised as `HostError` rather than unwinding into the interpreter.

A call stops when its context does. An in-flight `Instance` can also be interrupted from another goroutine with `Cancel`:

```go
ctx, cancel := context.WithTimeout(ctx, time.Second)
defer cancel()
_, err := p.Call(ctx, "maybe_forever") // context.DeadlineExceeded
```

## Compatability & Limitations

See the [MicroPython docs](https://docs.micropython.org/en/latest/genrst/index.html) for details on how it differs from CPython. 

Also, see [SUPPORT_MATRIX.md](./SUPPORT_MATRIX.md) for how a `micropython-go` instance differs from that of a traditional MicroPython build.

Functionally, this implementation differs due to:

- **No standard I/O or filesystem:** `import` cannot reach real files, `open()` raises `OSError`, `os` and `sys.stdout` are absent, and `print()` output is returned by `Exec` rather than written to stdout.
- **Stack depth:** recursion is bounded by the host C stack to roughly 340-385 Python frames, depending on how many arguments and locals each frame carries. Overflowing raises `RuntimeError` and leaves the interpreter usable. The limit is `MICROPY_C_STACK_SIZE` in `build/mpconfigport.h`, set to 96 KiB.
- **Structs via JSON:** scalars, maps, and slices use direct values; custom Go structs go through `encoding/json`. Prefer maps on hot paths.

## LLM/AI disclosure

Since I'm unfamiliar with C and the `clang` ecosystem, Claude Opus 5 on High Effort was used to assist. If anyone can see obvious issues (be idiomatic or some other scarier problem), contributions would be welcome!

## Contributing

Changing the C sources or build configuration means recompiling the WebAssembly module. The embedded MicroPython sources are `v1.28.0`. You need [wasi-sdk](https://github.com/WebAssembly/wasi-sdk) 25+ and [Binaryen](https://github.com/WebAssembly/binaryen), which makes the generated Go module safe for the garbage collector.

```bash
export WASI_SDK_PATH=/path/to/wasi-sdk-33.0
export BINARYEN_PATH=/path/to/binaryen

make       # builds out/guest.wasm, regenerates internal/micropython/micropython.go
make test
```