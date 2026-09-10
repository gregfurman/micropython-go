# Examples

Each directory is a self-contained program. Run one with `go run`:

```bash
go run ./examples/basic
```

Run `go test ./examples/...` to check the examples against their expected output.

| Example                          | Shows                                                                     |
| -------------------------------- | ------------------------------------------------------------------------- |
| [basic](./basic)                 | `Exec`, `Call`, `Eval` and `Get` on one stateful `Instance`               |
| [program](./program)             | Initializing once and running callbacks in parallel                       |
| [values](./values)               | Choosing the Python type an argument arrives as, and reading results back |
| [hostfunc](./hostfunc)           | Calling Go from Python, and raising a chosen exception class              |
| [channel](./channel)             | Backing a host function with a Go channel                                 |
| [callable](./callable)           | Holding a Python function in Go and passing it back to Python             |
| [iterator](./iterator)           | Pulling values from a generator one at a time                             |
| [stdout](./stdout)               | Collecting the guest's `print()` output, or streaming it as it runs       |
| [errors](./errors)               | Exceptions as Go errors, deadlines, and `Cancel`                          |
| [filesystem](./filesystem)       | Embedded files, Python imports, and a writable filesystem adapter         |
| [network](./network)             | Granting outbound TCP to one address and port                             |
| [memory](./memory)               | Sizing the Python heap and handling `MemoryError`                         |
