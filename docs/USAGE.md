# Usage guide

Start with the [quick start](./README.md#quick-start). For host access, see
[Sandboxing](./SANDBOXING.md). Complete runnable programs are in [examples](./examples).

## Contents

- [Instances](#instances)
- [Initialization](#initialization)
- [Programs](#programs)
- [Values](#values)
- [Calling Go from Python](#calling-go-from-python)
- [Errors and cancellation](#errors-and-cancellation)
- [Limitations](#limitations)

## Instances

An `Instance` keeps Python state between calls. Use `Exec` for statements,
`Eval` for expressions, `Call` for global functions, and `Get` or `Set` for
global variables. Close the instance when you are done.

It is safe to call from several goroutines, but a single interpreter runs one
call at a time, so concurrent calls queue rather than overlap. For parallelism,
give each worker its own interpreter with `Clone`, or use a `Program`.

## Initialization

`NewInstance` and `NewProgram` bind `WithGlobals` values and `WithHostFunc`
callbacks before executing `WithSource`, regardless of option order.
Pass ordinary Go values through `WithGlobals` instead of inserting them into
Python source.

After creation, use `Set`, `DefineFunction`, and `Exec` for the same operations.
For a Program, initialization runs once; each run starts from its resulting
Python state.

## Programs

A `Program` pools interpreters created from the initialized state.
Operations within one `Run` callback share Python state; separate runs do not
retain each other's changes. See the [Program example](./examples/program).

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

## Values

Pass ordinary Go values to `Call` and `Set`; `Call`, `Eval`, and `Get` return
`micropython.Value`s.

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

The following snippet assumes an existing instance `in` and context `ctx`.

```go
rates := map[string]float64{"EUR": 1.09, "GBP": 1.27} // Example rates.
err := in.DefineFunction(ctx, "usd", func(_ context.Context, args []micropython.Value) (micropython.Value, error) {
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
if _, err := in.Eval(ctx, `{"found": 1}["missing"]`); errors.As(err, &exc) {
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
