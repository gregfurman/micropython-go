# Sandboxing guide

See the [README example](../README.md#using-as-a-sandbox) for configuration.
All access options work with both `NewInstance` and `NewProgram`.

## Defaults

An interpreter starts with no filesystem access, an empty environment, no
networking or DNS, and discarded Python output. Built-in modules are available
without a filesystem. No Go callbacks are registered by default.

| Access                                  | Default                       | Option                           |
| --------------------------------------- | ----------------------------- | -------------------------------- |
| [Files](#files)                         | No access                     | `WithFS`                         |
| [Environment](#environment)             | Empty; no process inheritance | `WithEnv`                        |
| [Network connections](#network)         | Denied                        | `WithTCPAccess`, `WithUDPAccess` |
| [DNS](#network)                         | Denied                        | `WithDNSResolver`                |
| [Output](#output)                       | Discarded                     | `WithStdout`                     |
| [Go callbacks](#go-callbacks)           | None registered               | `WithHostFunc`                   |

## Files

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
See the [filesystem example](../examples/filesystem) for embedded files and its
[writable adapter](../examples/filesystem/writable_test.go) for wrapping `os.Root`.

## Environment

`WithEnv("STAGE", "prod")` sets a variable for `os.getenv`. Missing variables
return `None` or the supplied default. Python can change its own environment
with `os.putenv` and `os.unsetenv`; Programs restore the initialized environment
after each run.

## Network

`WithTCPAccess` and `WithUDPAccess` permit outbound connections to an IPv4
address, CIDR block, or `AnyAddress`, on one port. Grants are additive.
Hostnames and IPv6 are not supported in access rules. Use `WithDNSResolver`
to enable DNS separately; `net.DefaultResolver` uses the host resolver.

`AnyAddress` includes private and loopback addresses. Port 443 permits TCP
traffic, not just HTTPS, and DNS lookups are independent of connection grants.
Sockets are outbound only; UDP requires a connected peer. See the
[network example](../examples/network) for a narrower grant.

## Output

`WithStdout` sends `print()` output to an `io.Writer` owned by the caller.
Programs and clones share it, so concurrent runs need a writer that supports
concurrent writes; `bytes.Buffer` does not. See the [stdout example](../examples/stdout)
for buffering output or streaming it through a pipe.

## Go callbacks

`WithHostFunc` exposes a Go function to Python. A callback must enforce its own
access restrictions: connection grants and filesystem settings do not restrict
what its Go code can do. See [calling Go from Python](./USAGE.md#calling-go-from-python)
for error handling, concurrency, and reentrancy rules.

## Resource limits

`WithHeapSize` controls the Python heap, which defaults to 128 KiB. Exhausting
it raises `MemoryError`. It does not limit total interpreter or host memory,
including allocations made by callbacks and filesystem backends.

Pass a context with a deadline to interpreter operations for
[best-effort cancellation](./USAGE.md#errors-and-cancellation). Long C operations
and blocking host I/O can delay interruption. These options do not impose a
hard CPU or total-memory limit.

See the [memory example](../examples/memory) for heap sizing.

## Programs and shared resources

Programs and clones share the filesystem, output writer, Go callbacks, and DNS
resolver. Backends and callbacks must support concurrent use; do not modify a
shared resolver while it is in use. The caller owns these host resources.

Files, directory iterators, and sockets must be closed before Program
initialization finishes. Resources opened during a run are closed during
cleanup, but file changes, output, and Go callback state are not reset.
See [Program lifetimes](./USAGE.md#programs).
