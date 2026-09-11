# Embedded MicroPython for Go

<p align="center">
  <img src="./logo.png" alt="micropython-go" width="200">
</p>

[![Go Reference](https://pkg.go.dev/badge/github.com/gregfurman/micropython-go.svg)](https://pkg.go.dev/github.com/gregfurman/micropython-go)

`micropython-go` embeds [MicroPython](https://github.com/micropython/micropython)
in Go applications without CGO. Run Python scripts, exchange values, and call
Go functions from Python.

It uses a custom WebAssembly build of MicroPython, translated to native Go code
with [wasm2go](https://github.com/ncruces/wasm2go).

> [!IMPORTANT]
> This project is experimental. The API may change before a stable release.

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
}

// Output:
// 20
```

## Examples

Run the examples with `go run`. Their output is checked by
`go test ./examples/...`.

| Example                             | Shows                                                   |
| ----------------------------------- | ------------------------------------------------------- |
| [basic](./examples/basic)           | `Exec`, `Call`, `Eval` and `Get` against one `Instance` |
| [program](./examples/program)       | Initializing once and running callbacks in parallel     |
| [values](./examples/values)         | Choosing which Python type an argument arrives as       |
| [hostfunc](./examples/hostfunc)     | Calling back into Go, and raising a chosen exception    |
| [channel](./examples/channel)       | Backing a host function with a Go channel               |
| [callable](./examples/callable)     | Holding a Python function in Go and passing it back     |
| [iterator](./examples/iterator)     | Pulling values out of a generator one at a time         |
| [stdout](./examples/stdout)         | Collecting `print()` output, or streaming it            |
| [errors](./examples/errors)         | Exceptions as Go errors, deadlines, and `Cancel`        |
| [filesystem](./examples/filesystem) | Mounting an `fs.FS`, read-only and writable             |
| [network](./examples/network)       | Granting outbound TCP to one address and port           |
| [memory](./examples/memory)         | Sizing the Python heap, and `MemoryError`               |

```bash
go run ./examples/basic
```

## Instances

An `Instance` keeps Python state between calls. Use `Exec` for statements,
`Eval` for expressions, `Call` for global functions, and `Get` or `Set` for
global variables. Close the instance when you are done.

It is safe to call from several goroutines, but a single interpreter runs one
call at a time, so concurrent calls queue rather than overlap. For parallelism,
give each worker its own interpreter with `Clone`, or use a `Program`.

## Programs

A `Program` initializes Python once and saves that state. Each `Run` borrows
an interpreter from a pool and starts from the saved state, without rerunning
the initialization script. Operations within one callback share Python state;
separate runs do not retain each other's changes.

Use `WithSource` for initialization code and `WithGlobals` for its initial values.

The snippets below assume an existing `ctx`. See the examples above for complete
programs and imports.

```go
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

fmt.Println(out["id"], out["total"]) // r-1 13
```

Do not copy or retain the borrowed instance. Any goroutines using it must finish
before the callback returns. Copied data remains usable afterwards, but Python
object handles do not. `Export()` can still contain handles inside collections;
it does not detach those objects from the interpreter.

Use `Program.Instance` for a standalone interpreter that keeps state between
calls. You must close it separately.

`WithMaxIdle` limits idle interpreters, not concurrent runs. Limit concurrency
in your application to control peak memory use.

Files, directory iterators, and sockets must be closed before initialization
finishes. Resources opened during a run are closed during cleanup, and cleanup
errors are returned by `Run`. File changes, output, and other external effects
are not undone.

## Sandboxing

An interpreter starts with no filesystem access, an empty environment, no
networking or DNS, and discarded Python output. Built-in modules are available
without a filesystem. Options control host access, not hard CPU or total-memory
limits. Filesystem backends and Go callbacks must enforce their own access
restrictions.

For example, run untrusted code with one environment variable, a 256 KiB
Python heap, and a deadline of `1s`:

```go
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
fmt.Println(n) // 5050
```

Filesystem and network access remain denied. The Python heap defaults to 128 KiB
when `WithHeapSize` is omitted. The deadline provides
[best-effort cancellation](#errors-and-cancellation). The same access options
work with `NewProgram`.

| Access                                  | Default                       | Option                           |
| --------------------------------------- | ----------------------------- | -------------------------------- |
| [Files](#files)                         | No access                     | `WithFS`                         |
| [Environment](#environment)             | Empty; no process inheritance | `WithEnv`                        |
| [Network connections](#network)         | Denied                        | `WithTCPAccess`, `WithUDPAccess` |
| [DNS](#network)                         | Denied                        | `WithDNSResolver`                |
| [Output](#output)                       | Discarded                     | `WithStdout`                     |
| [Go callbacks](#calling-go-from-python) | None registered               | `WithHostFunc`                   |

### Files

`WithFS` exposes an `fs.FS`, such as `embed.FS`, at Python's root for `open()`
and file-based imports. Backends can grant writes through additional interfaces:

| Interface    | Grants                     |
| ------------ | -------------------------- |
| `OpenFileFS` | `open()` in a writing mode |
| `MkdirFS`    | `os.mkdir`                 |
| `UnlinkFS`   | `os.remove`                |
| `RmdirFS`    | `os.rmdir`                 |
| `RenameFS`   | `os.rename`                |

`ReadOnly` hides these write capabilities but does not add path confinement.
Backends must confine symlinks themselves; `os.DirFS` alone does not do this.
See the [filesystem example](./examples/filesystem) for embedded files and its
[writable adapter](./examples/filesystem/writable_test.go) for wrapping `os.Root`.

### Environment

`WithEnv("STAGE", "prod")` sets a variable for `os.getenv`. Missing variables
return `None` or the supplied default. Python can change its own environment
with `os.putenv` and `os.unsetenv`; Programs restore the initialized environment
after each run.

### Network

`WithTCPAccess` and `WithUDPAccess` permit outbound connections to an IPv4
address, CIDR block, or `AnyAddress`, on one port. Grants are additive.
Hostnames and IPv6 are not supported in access rules. DNS is enabled separately:

```go
in, err := micropython.NewInstance(ctx,
	micropython.WithTCPAccess(micropython.AnyAddress, 443), // AnyAddress == "*"
	micropython.WithDNSResolver(net.DefaultResolver),
)
if err != nil {
	log.Fatal(err)
}
defer in.Close()
```

`AnyAddress` includes private and loopback addresses. Port 443 permits TCP
traffic, not just HTTPS, and DNS lookups are independent of connection grants.
Sockets are outbound only; UDP requires a connected peer. See the
[network example](./examples/network) for a narrower grant.

### Output

`WithStdout` sends `print()` output to an `io.Writer` owned by the caller.
Programs and clones share it, so concurrent runs need a writer that supports
concurrent writes; `bytes.Buffer` does not. See the [stdout example](./examples/stdout)
for buffering output or streaming it through a pipe.

## Values

Pass ordinary Go values to `Call` and `Set`; `Call`, `Eval`, and `Get` return
`micropython.Value`s.
Use `WithGlobals` to supply configuration without inserting it into Python source.

### Go to Python

Arguments are converted recursively:

| Go value                         | Python value                                   |
| -------------------------------- | ---------------------------------------------- |
| `nil`                            | `None`                                         |
| `bool`                           | `bool`                                         |
| Signed integers                  | `int`                                          |
| Unsigned integers                | `int`; values above `math.MaxInt64` fail       |
| `*big.Int`                       | `int`; nil becomes zero                        |
| `float32`, `float64`             | `float`                                        |
| `string`                         | `str`                                          |
| `[]byte`                         | `bytes`                                        |
| Other slices and arrays          | `list`                                         |
| `map[string]struct{}`            | `set` of keys                                  |
| Other maps                       | `dict`                                         |
| Other pointers and interfaces    | The contained value, or `None` when nil        |
| Structs and other types          | JSON conversion; unsupported values fail       |

Use builders such as `Tuple` and `Set` when you need a specific Python type.
`Of` converts a Go value to a `Value` using the same rules. Use `BigInt` for
integers outside the `int64` range.

### Python to Go

`Export()` returns ordinary Go data where possible. The `As` methods check the
Python type and return an error on mismatch.

| Python value                       | `Export()` result                   | Checked access                          |
| ---------------------------------- | ----------------------------------- | --------------------------------------- |
| `None`                             | `nil`                               | `IsNone`                                |
| `bool`                             | `bool`                              | `AsBool`                                |
| `int`                              | `int64` or `*big.Int`               | `AsInt`, `AsBigInt`                     |
| `float`                            | `float64`                           | `AsFloat`                               |
| `str`                              | `string`                            | `AsString`                              |
| `bytes`                            | `[]byte`                            | `AsBytes`                               |
| `list`, `tuple`                    | `[]any`                             | `AsList`, `AsTuple`                     |
| `set`, `frozenset`                 | `[]any`                             | `AsSet`, `AsFrozenSet`                  |
| `dict` with only string keys       | `map[string]any`                    | `AsDict`                                |
| Other dictionaries                 | `map[any]any`                       | `AsDict`                                |
| Objects returned as handles        | `Value`                             | `AsObject`                              |

`AsInt` reports overflow; `AsFloat` requires a Python float, not an integer.
`Export()` stringifies dictionary keys that Go maps cannot hold, such as tuples.
Use `AsDict` to preserve the original keys. Exported collections can still
contain interpreter-backed handles.

Functions, iterators, and other Python objects may be returned as handles.
Use `Instance.AsCallable`, `Instance.AsIterator`, or `Instance.Resolve` to work
with them. Handles belong to their original interpreter. `Instance.Release`
optionally releases them early; copied data needs no release.

See the [values](./examples/values), [callable](./examples/callable), and
[iterator](./examples/iterator) examples for conversions and object lifetimes.

## Calling Go from Python

`DefineFunction` binds a Go function to a Python global. Use `WithHostFunc` to
register it before initialization, including when creating a `Program`.

```go
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
```

Use `Raise` to return a Python exception. Ordinary Go errors and recovered
panics become `HostError`, a subclass of `RuntimeError`.

Programs and clones share Go callbacks and their captured state. Callbacks must
support concurrent calls, and their Go state is not reset between runs. A callback
must not synchronously call or close the interpreter that invoked it.

## Errors and cancellation

A Python exception returns a Go error and normally leaves the interpreter
usable. Use `errors.As` to inspect it:

```go
var exc *micropython.PythonError
if _, err := in.Call(ctx, "lookup", "missing"); errors.As(err, &exc) {
	fmt.Println(exc.Type())    // KeyError
	fmt.Println(exc.Message()) // missing
	fmt.Println(exc.Raw())     // the traceback as MicroPython printed it
}
```

Pass a context with a deadline to interpreter operations to limit execution.
Cancellation returns the operation context's error. `Instance.Cancel` requests
a `KeyboardInterrupt` in the current Python operation.

Canceling a `Run` context also requests interruption of the current operation.
Pass that context to the borrowed methods as well so later calls observe its
cancellation. The Go callback must handle cancellation of its own work.

Cancellation is best effort. Long C operations, blocking filesystem calls, and
stdout writes can delay it. Host callbacks must cooperate with their context.

## Limitations

This build includes a subset of MicroPython's features and standard library.
It is not a drop-in replacement for CPython.

- **No `async` or `await`.** Both are a `SyntaxError` in this build.
- **Recursion is bounded.** Exceeding the limit raises `RuntimeError`.
- **The heap limit is not a total memory limit.** `WithHeapSize` controls the
  Python heap, not all interpreter and host allocations.

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
