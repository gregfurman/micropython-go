# Embedded MicroPython for Go

<p align="center">
  <img src="./logo.png" alt="micropython-go" width="200">
</p>

[![Go Reference](https://pkg.go.dev/badge/github.com/gregfurman/micropython-go.svg)](https://pkg.go.dev/github.com/gregfurman/micropython-go)

The `github.com/gregfurman/micropython-go` module is a `cgo`-free
[MicroPython](https://github.com/micropython/micropython) interpreter you can
embed directly in a Go program. Scripts run in-process, with ordinary Go values
crossing the boundary in both directions.

It wraps a [Wasm](https://webassembly.org/) build of MicroPython, using
[wasm2go](https://github.com/ncruces/wasm2go) to translate it into Go source.
Go and [`x/exp`](https://pkg.go.dev/golang.org/x/exp) are the _only_ required
dependencies: there is no C toolchain to install, no Python on the target
machine, and no machine code generated at runtime. `go build` compiles the
interpreter along with the rest of your program, and cross-compilation keeps
working.

> [!IMPORTANT]
> This project is still experimental. Until a tagged (and stable) version is
> released, both the public API and the internal modules are subject to
> breaking changes.

## Getting started

```go
import micropython "github.com/gregfurman/micropython-go"

in, _ := micropython.NewInstance(ctx)
defer in.Close()

in.Exec(ctx, "def scale(xs, by):\n    return [x * by for x in xs]\n")

got, _ := in.Call(ctx, "scale", []int64{1, 2, 3}, 10)
fmt.Println(got.Export()) // [10 20 30]
```

## Examples

The best way to learn micropython-go is to run one of the
[examples](./examples). The most [basic one](./examples/basic) loads a script
into an interpreter and calls into it.

| Example                         | Demonstrates                                                  |
| ------------------------------- | ------------------------------------------------------------- |
| [basic](./examples/basic)       | `Exec`, `Call`, `Eval` and `Get` against one stateful `Instance` |
| [program](./examples/program)   | Compiling a script once, then serving isolated calls in parallel |
| [values](./examples/values)     | Choosing which Python type an argument arrives as              |
| [hostfunc](./examples/hostfunc) | Calling back into Go, and raising a chosen exception class     |
| [package](./examples/package)   | Exposing Go functions as an importable Python package          |
| [callable](./examples/callable) | Holding a Python function in Go and passing it back            |
| [iterator](./examples/iterator) | Pulling values out of a generator one at a time                |
| [stdout](./examples/stdout)     | Collecting `print()` output, or streaming it as the script runs |
| [errors](./examples/errors)     | Exceptions as Go errors, deadlines, and `Cancel`               |

```bash
go run ./examples/basic
```

Every example is checked against its expected output, so `go test ./examples/...`
fails the moment one drifts from the library.

## Why embed Python?

Sooner or later a program has to run logic it did not ship with: a user-supplied
rule, a per-tenant scoring function, a plugin hook, an expression buried in a
config file. Rebuilding and redeploying every time that logic changes is a poor
trade, and the alternatives all charge you something for the privilege.

| Approach                                   | `cgo` | Runtime codegen | Cross-compiles                | Language           |
| ------------------------------------------ | ----- | --------------- | ----------------------------- | ------------------ |
| **micropython-go**                         | No    | No              | Yes, `go build`               | MicroPython        |
| `cgo` binding to CPython or MicroPython    | Yes   | No              | Needs a C toolchain per target | CPython or MicroPython |
| A Wasm runtime hosting a Python build      | No    | Yes, at startup | Yes                           | CPython or MicroPython |
| `os/exec` to a `python` binary             | No    | No              | Yes, if Python is installed   | CPython            |
| Starlark or Lua in Go                      | No    | No              | Yes                           | Not Python         |

Because the interpreter is translated to Go ahead of time rather than compiled
at startup, there is no warm-up and no runtime codegen. That matters on
platforms where generating machine code is not an option, and it means an
interpreter boots in roughly 80µs.

### When to choose micropython-go

It depends on your use case. If the guest is doing heavy computation, a real
CPython process will beat this comfortably: MicroPython is tuned for
microcontrollers, not for numerics, and there is no `numpy` waiting for you at
the bottom of the sandbox. If your program crosses the boundary often, passing
small values back and forth, then avoiding both `cgo` and a process boundary is
where this wins.

The other reason to reach for it is that the guest genuinely cannot escape.
There is no filesystem, no network, no subprocesses, and no clock beyond a
monotonic tick. Everything a script can reach outside itself, you handed it.

## Instances and Programs

An `Instance` is one interpreter that remembers what it is told. Variables,
imports and definitions survive from one call to the next, which is what you
want for a session, a REPL, or a long-lived worker. It is safe to use from
several goroutines, but a single interpreter runs one call at a time, so
concurrent callers queue rather than overlap.

A `Program` compiles a script once and serves calls from a pool of
interpreters. Each call borrows one, runs, and the interpreter is rewound to
the compiled state before it goes back to the pool.

```go
in, _ := micropython.NewInstance(ctx)       // stateful, sequential
p, _ := micropython.CompileSource(ctx, src) // isolated, concurrent
```

Rather than booting a fresh interpreter per call, `Compile` boots one, runs the
source, and snapshots its memory. Later interpreters are restored from that
snapshot instead of built from scratch, so every call starts from the
post-script state without re-running the module. Two calls therefore cannot see
each other's changes to Python state, which is what you want behind a request
handler.

Notice that rewinding is not a rollback of the outside world. Anything a call
did through a host function has already happened.

## Values

Arguments and results are a `micropython.Value`. Plain Go values work too, and
convert by the same rules, but where Go has one type for two Python ones the
builders let you say which you meant:

```go
in.Call(ctx, "f", []any{1, 2})                           // list
in.Call(ctx, "f", micropython.Tuple(micropython.Int(1))) // tuple
```

Conversion into Python is recursive, and applies to anything you pass:

| Go                               | Python                                   |
| -------------------------------- | ---------------------------------------- |
| `nil`                            | `None`                                   |
| `bool`                           | `bool`                                   |
| all signed and unsigned integers | `int`                                    |
| `float32`, `float64`             | `float`                                  |
| `string`                         | `str`                                    |
| `[]byte`                         | `bytes`                                  |
| other slices and arrays          | `list`                                   |
| maps                             | `dict`                                   |
| pointers and interfaces          | whatever they hold, or `None` when nil   |
| anything else, structs included  | JSON round trip, so a `dict` or a `list` |

The builders name the Python type outright: `None`, `Bool`, `Int`, `BigInt`,
`Float`, `Str`, `Bytes`, `List`, `Tuple`, `Set`, `FrozenSet` and `Dict`, plus
the `Strs` and `Ints` shorthands. `Of` takes the Go value you already have and
guesses, which is convenient right up until the guess is wrong.

On the way back, `Export` flattens to ordinary Go types, and the `As` methods
convert precisely, reporting a mismatch instead of panicking:

| Python                              | `Export`              | Typed accessor                              |
| ----------------------------------- | --------------------- | ------------------------------------------- |
| `None`                              | `nil`                 | `IsNone`                                    |
| `bool`                              | `bool`                | `AsBool`                                    |
| `int`                               | `int64` or `*big.Int` | `AsInt`, `AsBigInt`                         |
| `float`                             | `float64`             | `AsFloat`                                   |
| `str`                               | `string`              | `AsString`                                  |
| `bytes`                             | `[]byte`              | `AsBytes`                                   |
| `list`, `tuple`, `set`, `frozenset` | `[]any`               | `AsList`, `AsTuple`, `AsSet`, `AsFrozenSet` |
| `dict`                              | `map[string]any`      | `AsDict`                                    |
| anything else                       | an opaque handle      | `AsObject`, `AsCallable`, `AsIterator`      |

A dict whose keys are not all strings exports as a `map[any]any`, and a key that
a Go map cannot hold, such as a tuple, gets stringified to fit. `AsDict` sidesteps
both by handing back the pairs exactly as the guest sent them.

Anything with no Go equivalent, a class or a generator or a lambda, crosses as
an opaque handle rather than a copy. `AsCallable`, `AsIterator` and `Resolve`
turn one back into something usable.

Configuration is easier to bind than to splice into the source, where it would
have to be quoted and escaped:

```go
p, _ := micropython.CompileSource(ctx, src, micropython.WithGlobals(micropython.Globals{
	"NAME":   micropython.Str("service"),
	"LIMITS": micropython.Dict(micropython.Item{Key: micropython.Str("retries"), Val: micropython.Int(3)}),
}))
```

## Calling Go from Python

`DefineFunction` binds a Go function to a global Python name. Use `WithHostFunc`
instead when module-level code needs to call it, or when the target is a
`Program`, which registers the binding before it takes its snapshot.

```go
in.DefineFunction(ctx, "usd", func(_ context.Context, args []micropython.Value) (micropython.Value, error) {
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
```

An error returned from a host function raises at the Python call site. `Raise`
picks the class the guest catches; anything else becomes `HostError`, a class
this port adds under `RuntimeError` so guest code can single out host-boundary
failures without also swallowing the interpreter's own errors. A panic is
recovered and raised the same way rather than unwinding into the interpreter.

Bear in mind that a `Program` registers the closure once, before its snapshot,
so every pooled interpreter shares it. Whatever it closes over is Go state: the
per-call rewind does not reset it, and pooled interpreters may be inside it
concurrently.

`WithPackage` groups functions and values into something the guest imports,
nested subpackages included:

```go
micropython.Package("host",
	micropython.Attribute("service", micropython.Str("orders")),
	micropython.Package("text", micropython.Function("upper", upper)),
)
```

## Errors and cancellation

A guest that raises comes back as an ordinary Go error and leaves the
interpreter usable. Unwrap it to read which exception was raised, rather than
matching on the message:

```go
var exc *micropython.PythonError
if _, err := p.Call(ctx, "lookup", "missing"); errors.As(err, &exc) {
	fmt.Println(exc.Type())    // KeyError
	fmt.Println(exc.Message()) // missing
	fmt.Println(exc.Raw())     // the traceback as MicroPython printed it
}
```

A call stops when its context does, returning the context's error. An `Instance`
can also be interrupted from another goroutine with `Cancel`, which raises
`KeyboardInterrupt` in the running code.

Both are best effort. The request lands at the next VM hook, so a guest sitting
inside one long C-level operation, a regex match or a big-integer multiply, does
not stop until that finishes.

## Caveats

MicroPython is not CPython, and this build is not a stock MicroPython either. It
compiles at `MICROPY_CONFIG_ROM_LEVEL_MINIMUM` plus roughly forty explicit
flags, so the surface is smaller than either.

[`docs/SUPPORT_MATRIX.md`](./docs/SUPPORT_MATRIX.md) records the differences
against upstream, each verified by running the case in both interpreters rather
than by reading a config flag. The [MicroPython
docs](https://docs.micropython.org/en/latest/genrst/index.html) cover how
MicroPython itself differs from CPython.

The ones that catch people out:

- **No I/O or filesystem.** `import` reaches built-in modules only, `open()`
  raises `OSError`, and `print()` goes to the `WithStdout` writer or nowhere at
  all.
- **No `async` or `await`.** Both are a `SyntaxError` in this build.
- **Some builtins are missing**, `min`, `max` and `enumerate` among them. Each
  is a one-line flag in [`build/mpconfigport.h`](./build/mpconfigport.h) if you
  want it back.
- **Recursion is bounded** to roughly 340-385 Python frames by the host C stack
  (`MICROPY_C_STACK_SIZE`, 96 KiB). Overflowing raises a catchable
  `RuntimeError`.
- **Structs convert through JSON.** Scalars, maps and slices take a direct
  path, so prefer maps on hot paths.

Because each interpreter carries its own linear memory, memory usage is higher
than an in-process scripting language that shares the Go heap. The generated
interpreter is also around 2.7MB of Go source, which your build compiles once
and your binary then carries.

## Memory and performance

An interpreter costs about `0.5 MiB` of interpreter image plus its Python heap,
and none of it is shared. The heap defaults to `128 KiB`, putting a default
`Instance` at roughly `0.6 MiB`. Size it with `WithHeapSize` to what your script
actually allocates: too small and the guest raises `MemoryError`, which leaves
the interpreter usable.

`WithPoolSize` bounds how many interpreters a `Program` keeps idle, not how many
exist at once. A burst of 256 concurrent calls will still build 256; the surplus
is closed on release rather than refused, so peak memory follows your
concurrency.

Measured on an Apple M3 Pro with `go test -bench`, for a sense of scale rather
than as a promise:

| Operation                     | Cost    |
| ----------------------------- | ------- |
| `NewInstance`                 | ~80µs   |
| `CompileSource`               | ~195µs  |
| `Instance.Call`, no arguments | ~0.5µs  |
| `Program.Call`, no arguments  | ~12µs   |

`Program.Call` costs more because it rewinds the interpreter afterwards, and
rewinding copies the heap. Smaller heaps make isolated calls cheaper, which is
the main reason to bother tuning one.

## Contributing

Contributions are welcome, especially on the C and build tooling. I work
primarily in Go, and those parts were written with AI assistance.

Go changes need nothing beyond `go test ./...`. Changing the C sources or the
build configuration means recompiling the WebAssembly module and regenerating
its Go translation, which needs
[wasi-sdk](https://github.com/WebAssembly/wasi-sdk) 25+ and
[Binaryen](https://github.com/WebAssembly/binaryen). Binaryen's
`--spill-pointers` pass is what makes the generated module safe for Go's garbage
collector.

```bash
git submodule update --init            # MicroPython v1.28.0
export WASI_SDK=/path/to/wasi-sdk-25.0
export BINARYEN=/path/to/binaryen
./build/build.sh                       # regenerates internal/micropython
go test ./...
```

Both variables default to `tools/wasi-sdk` and `tools/binaryen`.

## License

[Apache 2.0](./LICENSE.md). MicroPython is MIT licensed; see the
[`micropython`](./micropython) submodule.
