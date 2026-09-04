# Examples

Each directory is a self-contained program. Run one with `go run`:

```bash
go run ./examples/basic
```

Every example is checked against its expected output by `go test ./examples/...`,
so what these print is what the library does.

| Example                          | Shows                                                                     |
| -------------------------------- | ------------------------------------------------------------------------- |
| [basic](./basic)                 | `Exec`, `Call`, `Eval` and `Get` on one stateful `Instance`               |
| [program](./program)             | Compiling once and serving concurrent, isolated calls from a pool         |
| [values](./values)               | Choosing the Python type an argument arrives as, and reading results back |
| [hostfunc](./hostfunc)           | Calling Go from Python, and raising a chosen exception class              |
| [package](./package)             | Exposing Go functions and values as an importable Python package          |
| [callable](./callable)           | Holding a Python function in Go and passing it back to Python             |
| [iterator](./iterator)           | Pulling values from a generator one at a time                             |
| [stdout](./stdout)               | Collecting the guest's `print()` output, or streaming it as it runs       |
| [errors](./errors)               | Exceptions as Go errors, deadlines, and `Cancel`                          |
