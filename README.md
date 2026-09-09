# Embedded MicroPython for Go

<p align="center">
  <img src="./logo.png" alt="micropython-go" width="200">
</p>

[![Go Reference](https://pkg.go.dev/badge/github.com/gregfurman/micropython-go.svg)](https://pkg.go.dev/github.com/gregfurman/micropython-go)

`micropython-go` is a CGO-free embeddable interpreter for [MicroPython](https://github.com/micropython/micropython).

It uses a custom [WebAssembly](https://webassembly.org/) (WASM) build of MicroPython, transpiled to native Go code using [wasm2go](https://github.com/ncruces/wasm2go).

> [!IMPORTANT]
> This project is still experimental. Until a tagged (and stable) version is
> released, both the public API and the internal modules are subject to
> breaking changes.

## Installation

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

Each example is a runnable program checked against its expected output, so
`go test ./examples/...` fails when one drifts from the library.

| Example                             | Shows                                                   |
| ----------------------------------- | ------------------------------------------------------- |
| [basic](./examples/basic)           | `Exec`, `Call`, `Eval` and `Get` against one `Instance` |
| [program](./examples/program)       | Compiling once, then serving calls in parallel          |
| [values](./examples/values)         | Choosing which Python type an argument arrives as       |
| [hostfunc](./examples/hostfunc)     | Calling back into Go, and raising a chosen exception    |
| [callable](./examples/callable)     | Holding a Python function in Go and passing it back     |
| [iterator](./examples/iterator)     | Pulling values out of a generator one at a time         |
| [stdout](./examples/stdout)         | Collecting `print()` output, or streaming it            |
| [errors](./examples/errors)         | Exceptions as Go errors, deadlines, and `Cancel`        |
| [filesystem](./examples/filesystem) | Giving the guest files to read                          |
| [network](./examples/network)       | Granting outbound TCP and DNS                           |
| [memory](./examples/memory)         | Heap sizing and what an interpreter costs               |

```bash
go run ./examples/basic
```

## Instances

An `Instance` is one long-running interpreter. State persists between calls, and
calls are fast because nothing is rewound between them.

```go
in, _ := micropython.NewInstance(ctx)
defer in.Close()

in.Exec(ctx, "total = 0")
in.Exec(ctx, "total += 5")

val, _ := in.Eval(ctx, "total * 2")
fmt.Println(val.Export()) // 10
```

It is safe to call from several goroutines, but a single interpreter runs one
call at a time, so concurrent calls queue rather than overlap. For parallelism,
give each worker its own interpreter with `Clone`, or use a `Program`.

## Programs

A `Program` compiles a script once and serves calls from a pool of interpreters.

Compiling boots one interpreter, runs the source, and snapshots its memory.
Later interpreters are restored from that snapshot rather than booted, so each
call starts from the post-script state without re-running the script. Two calls
cannot see each other's changes to Python state.

`Run` borrows an interpreter for the duration of a callback and rewinds it
afterwards, so copy out what you need before returning:

```go
p, err := micropython.Compile(ctx, `
def score(row):
    return {"id": row["id"], "total": row["a"] * 2 + row["b"]}
`)
if err != nil {
	log.Fatal(err)
}
defer p.Close()

var out map[string]any
err = p.Run(ctx, func(ctx context.Context, in *micropython.OwnedInstance) error {
	got, err := in.Call(ctx, "score", map[string]any{"id": "r-1", "a": 4, "b": 5})
	if err != nil {
		return err
	}
	out = got.Export().(map[string]any)
	return nil
})

fmt.Printf("%#v\n", out)
// map[string]interface {}{"id":"r-1", "total":13}
```

`Instance` gives you the interpreter itself when you want to hold it longer.

`WithMaxIdle` bounds how many interpreters the pool keeps, not how many exist at
once. A burst of concurrent calls builds as many as it needs and closes the
surplus on release, so peak memory follows your concurrency.

Rewinding is not a rollback of the outside world. Anything a call did through a
host function, a socket or a file has already happened.

## Capturing stdout

Nothing the guest prints escapes on its own. `print()` never reaches the
process's stdout; without `WithStdout` it is discarded.

```go
var out bytes.Buffer
in, _ := micropython.NewInstance(ctx, micropython.WithStdout(&out))
defer in.Close()

in.Exec(ctx, "print('hello')")
out.String() // "hello\n"
```

One sink serves the whole interpreter, and it is shared across calls, so reset
between them to read one call's output on its own.

Because it takes an `io.Writer`, the buffering decision stays yours. A pipe lets
a goroutine read alongside a running script rather than after it:

```go
pr, pw := io.Pipe()
in, _ := micropython.NewInstance(ctx, micropython.WithStdout(pw))

go func() {
	sc := bufio.NewScanner(pr)
	for sc.Scan() {
		log.Println("guest:", sc.Text())
	}
}()

in.Exec(ctx, script)
pw.Close() // the reader sees io.EOF
```

## Values

Arguments and results are a `micropython.Value`. Plain Go values work too and
convert by the same rules. Where Go has one type for two Python ones, the
builders let you say which you meant:

```go
in.Call(ctx, "f", []any{1, 2})                           // list
in.Call(ctx, "f", micropython.Tuple(micropython.Int(1))) // tuple
```

Conversion into Python is recursive and applies to anything you pass:

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
`Float`, `Str`, `Bytes`, `List`, `Tuple`, `Set`, `FrozenSet` and `Dict`. `Of`
takes a Go value and applies the table above.

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

A dict whose keys are not all strings exports as a `map[any]any`, and a key a Go
map cannot hold, such as a tuple, is stringified to fit. `AsDict` hands back the
pairs exactly as the guest sent them.

Anything with no Go equivalent, such as a class, generator or lambda, crosses as
an opaque handle rather than a copy. `AsCallable`, `AsIterator` and `Resolve`
turn one back into something usable.

Configuration is easier to bind than to splice into the source, where it would
have to be quoted and escaped:

```go
p, _ := micropython.Compile(ctx, src, micropython.WithGlobals(micropython.Globals{
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
picks the class the guest catches. Anything else becomes `HostError`, a class
this port adds under `RuntimeError` so guest code can catch host-boundary
failures without also catching the interpreter's own errors. A panic is
recovered and raised the same way rather than unwinding into the interpreter.

A `Program` registers the closure once, before its snapshot, so every pooled
interpreter shares it. Whatever it closes over is Go state: the per-call rewind
does not reset it, and pooled interpreters may be inside it at the same time.

## Files, environment and network

A guest starts with none of these. Each is granted by an option, and without one
the corresponding Python call raises `OSError`.

`WithFS` mounts an `fs.FS` at the guest's root, so `open()` and `import` can
reach it:

```go
//go:embed scripts
var scripts embed.FS

in, _ := micropython.NewInstance(ctx, micropython.WithFS(scripts))
in.Exec(ctx, "print(open('/scripts/config.json').read())")
```

An `fs.FS` is read-only. A backend grants writes by implementing the matching
interface, so there is no flag to get wrong:

| Interface    | Grants                     |
| ------------ | -------------------------- |
| `OpenFileFS` | `open()` in a writing mode |
| `MkdirFS`    | `os.mkdir`                 |
| `UnlinkFS`   | `os.remove`                |
| `RmdirFS`    | `os.rmdir`                 |
| `RenameFS`   | `os.rename`                |

`os.Root` from the standard library is the usual writable backend, since it
confines symlinks to its directory. `ReadOnly` wraps a backend to hide those
methods again.

`WithEnv` sets one variable that `os.getenv` reads. The environment is the
instance's own and is never seeded from the Go process, so a variable reaches
the guest only by being named here:

```go
micropython.WithEnv("STAGE", "prod")
```

`WithTCPAccess` and `WithUDPAccess` permit outbound connections to an address,
CIDR block, or `AnyAddress`, on one port. Grants are additive. Name resolution
is separate and also off by default:

```go
in, _ := micropython.NewInstance(ctx,
	micropython.WithTCPAccess(micropython.AnyAddress, 443),
	micropython.WithDNSResolver(net.DefaultResolver),
)
```

Sockets are outbound only. `bind`, `listen` and `accept` raise `OSError`.

## Errors and cancellation

A guest that raises comes back as an ordinary Go error and leaves the
interpreter usable. Unwrap it to read which exception was raised, rather than
matching on the message:

```go
var exc *micropython.PythonError
if _, err := in.Call(ctx, "lookup", "missing"); errors.As(err, &exc) {
	fmt.Println(exc.Type())    // KeyError
	fmt.Println(exc.Message()) // missing
	fmt.Println(exc.Raw())     // the traceback as MicroPython printed it
}
```

A call stops when its context does, returning the context's error. An `Instance`
can also be interrupted from another goroutine with `Cancel`, which raises
`KeyboardInterrupt` in the running code.

Both are best effort. The request lands at the next VM hook, so a guest inside
one long C-level operation, such as a regex match or a big-integer multiply,
does not stop until that finishes.

## Limitations

MicroPython is not CPython, and this build is not a stock MicroPython either. It
compiles at `MICROPY_CONFIG_ROM_LEVEL_MINIMUM` plus roughly forty explicit
flags, so the surface is smaller than either. The [MicroPython
docs](https://docs.micropython.org/en/latest/genrst/index.html) cover how
MicroPython itself differs from CPython.

The ones that catch people out:

- **No `async` or `await`.** Both are a `SyntaxError` in this build.
- **Some builtins are missing**, `min`, `max` and `enumerate` among them. Each
  is a one-line flag in [`build/mpconfigport.h`](./build/mpconfigport.h).
- **Recursion is bounded** to roughly 340-385 Python frames by the host C stack
  (`MICROPY_C_STACK_SIZE`, 96 KiB). Overflowing raises a catchable
  `RuntimeError`.
- **Structs convert through JSON.** Scalars, maps and slices take a direct
  path, so prefer maps on hot paths.

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
