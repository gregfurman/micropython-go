# MicroPython for Go (WASI)

A pure-Go embedding of MicroPython. The interpreter is compiled to WebAssembly and translated to Go with `wasm2go`.

Embed a full MicroPython interpreter directly in a Go application. It uses no CGO and no external WebAssembly runtime such as wazero or wasmtime.

* **No CGO required:** Runs entirely in native Go.
* **Isolated:** The guest has no filesystem or OS access. It reaches Go only through functions you register yourself.
* **Fast:** Reused interpreters avoid snapshot work; isolated programs trade some throughput for state isolation.
* **Concurrent:** Built-in pooling allows parallel Python execution across goroutines.

## Installation

```bash
go get github.com/gregfurman/micropython-go
```

## Quick Start

The library provides two ways to run Python: **Programs** (stateless, pooled, and parallel) and **Instances** (stateful and sequential).

### 1. Programs (Stateless & Concurrent)

If you want to define a Python function once and call it thousands of times across multiple goroutines, compile a `Program`.

A `Program` takes a snapshot of the interpreter and rewinds memory after every call, guaranteeing perfect isolation.

This means **module-level state does not accumulate**. A global the script defines is back at its starting value for the next call, and one a function assigns to is discarded. Globals bound with `WithGlobals` do survive, because they are set before the script runs and so are part of what every call starts from. If you need state to carry between calls, use an `Instance`.

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

	// Compile the Python script once. This creates an internal pool of interpreters.
	p, err := micropython.Compile(ctx, `
def score(row):
    total = row["a"] * 2 + row["b"]
    return {"id": row["id"], "score": total, "ok": total > 10}
`)
	if err != nil {
		log.Fatal(err)
	}
	defer p.Close()

	// Call the Python function from Go.
	// This is safe to run concurrently across multiple goroutines!
	got, err := p.Call(ctx, "score", map[string]any{"id": "r-1", "a": 4, "b": 5})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%#v\n", got)
	// Output: map[string]interface {}{"id":"r-1", "ok":true, "score":13}
}

```

### 2. Instances (Stateful)

If you need a long-running interpreter where variables and state persist between evaluations, use an `Instance`. It is also much faster per call, since nothing is rewound between them.

```go
ctx := context.Background()

in, _ := micropython.NewInstance(ctx)
defer in.Close()

in.Exec(ctx, "total = 0")
in.Exec(ctx, "total += 5")

val, _ := in.Eval(ctx, "total * 2")
fmt.Println(val) // Output: 10

```

### 3. Passing data in

Arguments are ordinary Go values. When Go's type does not preserve the desired Python distinction—for example, a slice could mean a `list` or a `tuple`—use the public value builders:

```go
p.Call(ctx, "f", []any{1, 2})                              // list
p.Call(ctx, "f", micropython.Tuple(micropython.Int(1)))    // tuple
```

Configuration can be bound as globals rather than spliced into the source, which avoids quoting anything:

```go
p, err := micropython.Compile(ctx, src, micropython.WithGlobals(micropython.Globals{
    "NAME":   micropython.Str("service"),
    "LIMITS": micropython.Dict(micropython.Item{Key: micropython.Str("retries"), Val: micropython.Int(3)}),
}))
```

A guest that raises comes back as an ordinary Go error, and leaves the interpreter usable. Unwrap it to branch on which exception it was:

```go
var exc *micropython.PythonError
if _, err := p.Call(ctx, "lookup", "missing"); errors.As(err, &exc) {
    fmt.Println(exc.Type())    // KeyError
    fmt.Println(exc.Message()) // missing
    fmt.Println(exc.Raw())     // the traceback as MicroPython printed it
}
```

A call stops when its context does. You can also interrupt an in-flight `Instance` from another goroutine with `Cancel`:

```go
ctx, cancel := context.WithTimeout(ctx, time.Second)
defer cancel()
_, err := p.Call(ctx, "maybe_forever") // context.DeadlineExceeded
```

### 4. Defining host functions with Go

`DefineFunction` binds a Go function to a global Python name, so the guest can call out to fetch data, consult a cache, or reach anything else the interpreter has no access to on its own. Nothing is reachable unless you register it.

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

Arguments arrive already converted to native Go values, and the return value is converted back, both following the Type Conversion table below. A Python `list` or `tuple` arrives as `[]any`, a `dict` as a `map[any]any`, and returning a `Value` built with `Of`, `Tuple` or `Str` lets you pick the Python type exactly.

A binding is part of interpreter state, so it lasts for the life of the `Instance` and any `Clone` taken afterwards inherits it. Redefining a name replaces it. `Program` has no equivalent: its calls are rewound to a snapshot, so host functions belong to an `Instance`.

#### Failure

Returning an error raises an exception at the Python call site. An ordinary Go error becomes `HostError`, a class this port adds so guest code can single out host-boundary failures:

```python
try:
    fetch()
except HostError as e:
    print("host call failed:", e)
```

`HostError` subclasses `RuntimeError`, so an existing `except RuntimeError` handler still catches a failed host call and you only reach for the narrower name when you want to tell the two apart.

To raise something Python already understands, return `micropython.Raise`, which resolves against the builtin exceptions and falls back to `HostError` if the name is not one of them:

```go
return nil, micropython.Raise("KeyError", code)   // guest catches KeyError
```

A panic inside the function is recovered and raised as `HostError` rather than unwinding into the interpreter. Either way the `Instance` stays usable, and the failure reaches Go as a `*PythonError` carrying the same type and message.

## Type Conversion

Values cross the Go/Wasm boundary as direct tagged values. Ordinary Go maps, slices, and scalar values are converted recursively; custom structs use an `encoding/json` fallback.

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


## Building the Wasm Module (For Contributors)

If you are modifying the underlying C code or the MicroPython build configuration, you will need to recompile the WebAssembly module.

The embedded MicroPython sources are version `v1.28.0`. You need [wasi-sdk](https://github.com/WebAssembly/wasi-sdk) (25+) and [Binaryen](https://github.com/WebAssembly/binaryen). Binaryen is required to make the generated Go module safe for the Go garbage collector.

```bash
# Point to your WASI SDK and Binaryen installations.
export WASI_SDK_PATH=/path/to/wasi-sdk-33.0
export BINARYEN_PATH=/path/to/binaryen

# Build out/guest.wasm and regenerate internal/micropython/micropython.go.
make

# Run the Go tests.
make test

```

## Heap size

The Python heap is most of an interpreter's allocation cost. `Program` also copies it when restoring a snapshot, so smaller heaps reduce isolated-call cost. The default is 2 MiB and can be set per `Program` or `Instance`:

```go
p, err := micropython.Compile(ctx, src, micropython.WithHeapSize(256*1024))
```

Too small and the guest raises `MemoryError` and the `Program` stays usable. Size it to what your script actually allocates.

The Wasm shadow stack is fixed at build time to 1 MiB. Recursion is bounded well before that by `MICROPY_C_STACK_SIZE`, which this port sets to 96 KiB in `build/mpconfigport.h`.

## Limitations

* **No standard I/O or filesystem:** `import` cannot reach real files, `open()` raises `OSError`, `os` and `sys.stdout` are absent, and `print()` output is returned by `Exec` rather than written to stdout.
* **Stack depth:** recursion is bounded by the host C stack to roughly 340-385 Python frames, depending on how many arguments and locals each frame carries. Overflowing raises `RuntimeError` and leaves the interpreter usable.
* **Structs via JSON:** scalars, maps, and slices use direct values; custom Go structs go through `encoding/json`. Prefer maps on hot paths.
