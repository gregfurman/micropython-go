# Embedded MicroPython for Go

<p align="center">
  <img src="./logo.png" alt="micropython-go" width="200">
</p>

[![Go Reference](https://pkg.go.dev/badge/github.com/gregfurman/micropython-go.svg)](https://pkg.go.dev/github.com/gregfurman/micropython-go)
[![Test](https://github.com/gregfurman/micropython-go/actions/workflows/test.yaml/badge.svg?branch=main)](https://github.com/gregfurman/micropython-go/actions/workflows/test.yaml)
[![Build](https://github.com/gregfurman/micropython-go/actions/workflows/build.yaml/badge.svg?branch=main)](https://github.com/gregfurman/micropython-go/actions/workflows/build.yaml)

`micropython-go` embeds [MicroPython](https://github.com/micropython/micropython)
in Go applications without CGO. Run Python scripts, exchange values, and call
Go functions from Python.

It uses a custom WebAssembly build of MicroPython, translated to native Go code
with [wasm2go](https://github.com/ncruces/wasm2go).

> [!IMPORTANT]
> This project is experimental. The API may change before a stable release.

## Installation

Requires Go 1.27 or later. You do not need the WebAssembly build tools to use
the SDK.

```bash
go get github.com/gregfurman/micropython-go
```

## Quick start

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

	// Create an interpreter.
	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	// Release resources when done.
	defer in.Close()

	// Define a Python function.
	if err := in.Exec(ctx, "double = lambda x: x * 2"); err != nil {
		log.Fatal(err)
	}

	// Call it from Go.
	got, err := in.Call(ctx, "double", 10)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(got.Export())
}

// Output:
// 20
```

## Execution

Use an `Instance` for a persistent Python session, or a `Program` to run
repeatedly from the same initialized state.

Snippets below assume an existing `ctx` and the relevant imports. Error handling
is omitted for brevity except inside callbacks; check errors before using
returned resources in your application.

### Instance

An `Instance` keeps Python state between calls. Create one with
`NewInstance(ctx)` and close it when done. Calls are serialized; use separate
instances for parallel execution.

```go
in, _ := micropython.NewInstance(ctx,
	micropython.WithSource("def double(x): return x * 2"),
)
defer in.Close()

got, _ := in.Call(ctx, "double", 10)
fmt.Println(got.Export()) // 20
```

### Program

A `Program` initializes Python once and saves its state. Each `Run` borrows
an interpreter starting from that state, then resets it afterwards.
Runs can execute concurrently.

```go
p, _ := micropython.NewProgram(ctx,
	micropython.WithSource("def double(x): return x * 2"),
)
defer p.Close()

err := p.Run(ctx, func(in *micropython.BorrowedInstance) error {
	got, err := in.Call(ctx, "double", 10)
	if err != nil {
		return err
	}
	fmt.Println(got.Export()) // 20
	return nil
})
```

Do not retain the borrowed instance or Python object handles after the callback
returns. External effects, such as file writes, are not undone.
See [Program lifetimes and pooling](./docs/USAGE.md#programs).

## Configuration

### Initialization

Use options to set globals, register Go callbacks, and run initialization code.
The same options work with `NewInstance` and `NewProgram`.

```go
// Define your Go callback
louder := func(_ context.Context, args []micropython.Value) (micropython.Value, error) {
	if len(args) != 1 {
		return micropython.Value{}, micropython.Raise("ValueError", "expected a single arg")
	}
	msg, err := args[0].AsString()
	if err != nil {
		return micropython.Value{}, err
	}
	return micropython.Str(strings.ToUpper(msg)), nil
}

// Create an Instance
in, _ := micropython.NewInstance(ctx,
	micropython.WithGlobals(micropython.Globals{"LOCATION": "New York"}), // define a global
	micropython.WithHostFunc("host_louder", louder),                      // expose a Go callback
	micropython.WithSource(`def loud_greeting(): return host_louder("hello from " + LOCATION)`),
)
defer in.Close()

got, _ := in.Call(ctx, "loud_greeting")
fmt.Println(got.Export()) // HELLO FROM NEW YORK
```

See [initialization](./docs/USAGE.md#initialization) and
[calling Go from Python](./docs/USAGE.md#calling-go-from-python).

### Using as a sandbox

By default, Python has no access to host files, environment variables, output,
or networking. Grant only the capabilities your code needs:

```go
root, _ := os.OpenRoot("./sandbox") // directory must already exist
defer root.Close()

in, _ := micropython.NewInstance(ctx,
	micropython.WithStdout(os.Stdout),                      // send print() to host stdout
	micropython.WithEnv("ENV", "dev"),                      // set a Python environment variable
	micropython.WithFS(root.FS()),                          // read-only access to ./sandbox at /
	micropython.WithTCPAccess("127.0.0.1", 8000),           // allow outbound TCP to this address and port
	micropython.WithTCPAccess(micropython.AnyAddress, 443), // allow outbound TCP to any IPv4 address on port 443
	micropython.WithDNSResolver(net.DefaultResolver),       // enable DNS resolution
	micropython.WithHeapSize(256*1024),                     // Python heap size in bytes
)
defer in.Close()
```

These options also work with `NewProgram`. `AnyAddress` includes private networks;
port 443 is not an HTTPS-only restriction. The heap size is not a total-memory
limit, and cancellation is best effort. See the [sandboxing guide](./docs/SANDBOXING.md)
for access rules and resource limits.

## Go and Python values

Pass ordinary Go values to Python; results come back as `micropython.Value`.
For example, using an existing instance `in`:

```go
in.Set(ctx, "numbers", []int{1, 2, 3}) // Go slice → Python list
got, _ := in.Eval(ctx, "sum(numbers)")
n, _ := got.AsInt()                  // Python int → Go int64
fmt.Println(n)                      // 6
```

Use `Export()` for ordinary Go data, or methods such as `AsInt()` and
`AsString()` for checked conversions. See the [conversion tables](./docs/USAGE.md#values)
for supported types and object handles.

## Documentation and examples

- [Usage guide](./docs/USAGE.md): lifetimes, value conversions, callbacks, and cancellation.
- [Sandboxing guide](./docs/SANDBOXING.md): files, environment, networking, and resource limits.
- [API reference](https://pkg.go.dev/github.com/gregfurman/micropython-go): all public types and options.

| Example                             | Shows                                |
| ----------------------------------- | ------------------------------------ |
| [Basics](./examples/basic)          | Execute Python and exchange values   |
| [Programs](./examples/program)      | Initialize once and run concurrently |
| [Values](./examples/values)         | Convert between Go and Python types  |
| [Go callbacks](./examples/hostfunc) | Call Go functions from Python        |
| [Files](./examples/filesystem)      | Expose a filesystem to Python        |
| [Networking](./examples/network)    | Grant outbound connections           |

See [all examples](./examples) for output, cancellation, object handles, and memory sizing.

```bash
go run ./examples/basic
```

This build supports a subset of MicroPython, not full CPython.
See [limitations](./docs/USAGE.md#limitations).

## Why this project?

As the old programming adage goes: "Never trust user input.", the obvious corollary
being "Never trust arbitrary executable code supplied by anyone, ever, AT ANY
TIME." While the latter doesn't roll off the tongue quite as nicely, this is a
real issue for projects wanting to let users supply code or custom scripts
without compromising the host application.

I wanted a sandboxed way for users to execute Python(-ic) code within a Go
application, without the overhead of embedding CPython—and so `micropython-go`
was born 🐍🦫 [^1]

- **Why MicroPython?** Designed initially for embedded systems and
  microcontrollers, MicroPython strikes a brilliant balance between resource
  efficiency and feature richness. While CPython _could_ do the job, I wanted a
  smaller dependency for my Go applications.

- **Why Python rather than another scripting language?** Projects like
  [Common Expression Language](https://github.com/cel-expr/cel-go) (CEL) offer
  Go-native ways to evaluate user-supplied expressions. I wanted Python-like
  syntax because it's familiar to many engineers, which means one less
  documentation site to frequent. Transpiling MicroPython to Go also lets me
  build on an existing language rather than maintain a new one.

- **Why not just use a WASM runtime?** I wanted the interpreter to fit naturally
  into a Go application, including how it exposes host functionality.
  Transpiling to Go lets me do that without shipping a separate WASM runtime.

- **Why this project versus the alternatives?** I've yet to see another project
  maintainer spend their entire weekend trying to get a
  [gopher snake onto a gopher's head](#embedded-micropython-for-go)… If that
  didn't convince you, hopefully the small memory footprint, Go/Python value
  conversions, and sandboxing functionality will.

## Contributing

Contributions are welcome, especially on the C and build tooling. I work
primarily in Go, and those parts were written with AI assistance.

Go changes need nothing beyond `go test ./...`. Changing the C sources or the
build configuration means recompiling the WebAssembly module and regenerating
its Go translation, which needs [wasi-sdk](https://github.com/WebAssembly/wasi-sdk)
and [Binaryen](https://github.com/WebAssembly/binaryen).

```bash
git submodule update --init
export WASI_SDK=/path/to/wasi-sdk
export BINARYEN=/path/to/binaryen
./build/build.sh                       # regenerates internal/micropython
go test ./...
```

Both variables default to `tools/wasi-sdk` and `tools/binaryen`.

## License

[Apache 2.0](./LICENSE.md). MicroPython is MIT licensed; see the
[`micropython`](./micropython) submodule.


[^1]: I'm aware this is a beaver and not a gopher, but an artist is limited by their medium.
