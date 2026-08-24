# MicroPython for Go (WASI)

A pure-Go port of MicroPython, compiled to WebAssembly and translated to Go via `wasm2go`.

This package allows you to embed a full MicroPython interpreter directly into your Go applications. Because it is pure Go (no CGO) and requires no external WebAssembly runtime (like wazero or wasmtime), it is incredibly fast, easy to deploy, and completely sandboxed.

* **No CGO required:** Runs entirely in native Go.
* **No WASI dependencies:** Does not require a filesystem, standard I/O, or OS access.
* **Fast:** ~1.5M calls/sec on a reused interpreter; ~24k/sec when each call is isolated (see [Performance](#performance)).
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

Arguments are ordinary Go values. Where Go has one type for two Python, e.g a slice could be a `list` or a `tuple`, build the value instead using the public SDK:

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

A call stops when its context does, which is the only thing that bounds a runaway guest:

```go
ctx, cancel := context.WithTimeout(ctx, time.Second)
defer cancel()
_, err := p.Call(ctx, "maybe_forever") // context.DeadlineExceeded
```

## Type Conversion

Go and Python types cross as a compact binary format rather than JSON, except for custom structs, which take a JSON round trip.

| Go Type | Python Type |
| --- | --- |
| `nil` | `None` |
| `bool` | `bool` |
| `int`, `int64`, etc. | `int` |
| `float64` | `float` |
| `string` | `str` |
| `[]byte` | `bytes` |
| `[]any`, `[]string`, `[]int` | `list` |
| `map[string]any` | `dict` |
| `micropython.Tuple(...)` | `tuple` |
| `micropython.Set(...)` | `set` |
| `struct{...}` | *JSON round-trip to* `dict` |
| `micropython.Of(v)` | whatever the rules above make of `v`, explicitly |

Results come back the same way. A Python `tuple` arrives as `micropython.Tuple` and a `set` as `micropython.Set` -- distinct slice types, so a tuple is still recognisable as one.

## Building the Wasm Module (For Contributors)

If you are modifying the underlying C code or the MicroPython build configuration, you will need to recompile the WebAssembly module.

MicroPython is included as a submodule pinned to `v1.28.0`. You will need [wasi-sdk](https://github.com/WebAssembly/wasi-sdk) (25+) and [Binaryen](https://github.com/WebAssembly/binaryen) installed.

```bash
# 1. Point to your WASI SDK and Binaryen installations
export WASI_SDK_PATH=/path/to/wasi-sdk-33.0
export BINARYEN_PATH=/path/to/binaryen

# 2. Build the out/micropython.wasm binary
make

# 3. Translate the .wasm file into pure Go using wasm2go
make wasm2go

# 4. Run the Go tests to verify
make test

```

## Performance

Measured on an M3 Pro, `go test -bench`.

| | calls/sec |
| --- | --- |
| `Instance.Call` (state persists) | ~1,500,000 |
| `Program.Call` (isolated, 2 MB heap) | ~25,000 |
| `Program.Call` (isolated, 256 KB heap) | ~147,000 |

The gap is the isolation: a `Program` rewinds the interpreter's memory after every call so no call can see what another did, and that copy is proportional to the heap.

### Tuning the heap

The Python heap is most of what an interpreter costs to create, and to rewind between calls via snapshotting. It defaults to 2 MB and is set per `Program` or `Instance`:

```go
p, err := micropython.Compile(ctx, src, micropython.WithHeapSize(256*1024))
```

| heap | `Program.Call` | calls/sec |
| --- | --- | --- |
| 256 KB | 6.8 µs | ~147,000 |
| 512 KB | 11.7 µs | ~85,000 |
| 1 MB | 20.8 µs | ~48,000 |
| 2 MB (default) | 39.5 µs | ~25,000 |

Too small and the guest raises `MemoryError` and the `Program` stays usable. Size it to what your script actually allocates.

The shadow stack is fixed at build time (`WASM_STACK_SIZE`) because the linker places it, and it bounds recursion depth together with `MICROPY_C_STACK_SIZE`.

## Limitations

* **No standard I/O or filesystem:** `import` cannot reach real files, `open()` raises `OSError`, `os` and `sys.stdout` are absent, and `print()` output is captured by `Exec` rather than written to `stdout`.
* **Stack depth:** recursion is bounded by the host C stack to about 359 Python frames at the default `MICROPY_C_STACK_SIZE`.
* **Structs via JSON:** primitives, maps and slices use the binary format; custom Go structs go through `encoding/json`. Use `map[string]any` on a hot path.
* **No host functions:** Python cannot call back into Go.