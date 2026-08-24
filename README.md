# MicroPython for Go (WASI)

A pure-Go port of MicroPython, compiled to WebAssembly and translated to Go via `wasm2go`.

This package allows you to embed a full MicroPython interpreter directly into your Go applications. Because it is pure Go (no CGO) and requires no external WebAssembly runtime (like wazero or wasmtime), it is incredibly fast, easy to deploy, and completely sandboxed.

* **No CGO required:** Runs entirely in native Go.
* **No WASI dependencies:** Does not require a filesystem, standard I/O, or OS access.
* **Fast:** Capable of ~500,000 Python function calls per second.
* **Concurrent:** Built-in pooling allows parallel Python execution across goroutines.

## Installation

```bash
go get github.com/gregfurman/micropython-wasi

```

## Quick Start

The library provides two ways to run Python: **Programs** (stateless, pooled, and parallel) and **Instances** (stateful and sequential).

### 1. Programs (Stateless & Concurrent)

If you want to define a Python function once and call it thousands of times across multiple goroutines, compile a `Program`.

A `Program` takes a snapshot of the interpreter and rewinds memory after every call, guaranteeing perfect isolation.

```go
package main

import (
	"context"
	"fmt"
	"log"

	micropython "github.com/gregfurman/micropython-wasi"
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

If you need a long-running interpreter where variables and state persist between evaluations, use an `Instance`.

```go
in, _ := micropython.NewInstance(context.Background())
defer in.Close()

in.Exec(ctx, "total = 0")
in.Exec(ctx, "total += 5")

val, _ := in.Eval(ctx, "total * 2")
fmt.Println(val) // Output: 10

```

## Type Conversion

Go and Python types cross the boundary seamlessly without paying the cost of JSON serialization (unless you pass a custom Go struct).

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
| `struct{...}` | *JSON round-trip to `dict*` |

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

## Limitations

* **No standard I/O or Filesystem:** `import` cannot reach real files, `open()` does not exist, and `print()` output is buffered rather than written to `stdout`.
* **Stack Depth:** Python recursion depth is bounded by the host stack size.
* **Structs via JSON:** While primitives, maps, and slices use a highly optimized zero-copy binary format to cross the boundary, custom Go structs are serialized via `encoding/json`. Use `map[string]any` for hot-path data.