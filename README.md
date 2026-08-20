# The WASI port

MicroPython as an embeddable Wasm module, built with [wasi-sdk] and
**without using the Wasm exception handling proposal**, for calling Python
functions from Go via `wasm2go`.

It is built against the `wasm32-wasip1` sysroot, but the finished module
imports nothing from `wasi_snapshot_preview1` — there is no filesystem, no
stdio, no argv, and no REPL. What it imports is two host interfaces of its own:
the `setjmp`/`longjmp` trampolines, and the callbacks a Python value is handed
back through. Everything else is driven by the host calling exports.

This is separate from `ports/webassembly`, which is Emscripten-specific and
assumes a JavaScript host.

## Building

MicroPython itself is a submodule, pinned to v1.28.0. `make` syncs it to the
pinned revision before building, so a fresh checkout needs no separate step.

Point the build at an unpacked [wasi-sdk] (25 or newer), either with
`WASI_SDK_PATH` or by unpacking one next to the Makefile. [Binaryen] is also
required, for `wasm-opt --spill-pointers` (see below):

    $ export WASI_SDK_PATH=/path/to/wasi-sdk-33.0-arm64-macos
    $ export BINARYEN_PATH=/opt/homebrew/opt/binaryen
    $ make

That produces `out/micropython.wasm` (~374k).

The C sources live in `build/`, following the layout `go-sqlite3-wasm` uses;
`out/` is only build output and is the one thing gitignored.

    $ make wasm2go   # translate the module to Go
    $ make test      # go test ./...

## Running it

The module is a **reactor**: there is no `main()`. The host calls
`_initialize`, which runs the constructor that boots the interpreter, and from
then on drives it through the eleven exports in `wasm_api.h`.

It imports two modules, both of which the host must supply:

- `env` — the `invoke_*` trampolines and `_emscripten_throw_longjmp` that
  `setjmp`/`longjmp` is lowered onto. That is the price of not using exception
  handling; the section below explains why.
- `host` — the `val_*` callbacks a Python value is streamed through on its way
  out.

`internal/env` and `internal/host` implement them.

## Embedding it

The shape this is built for is a function defined once and invoked many times:

```go
in, _ := host.New()

score, _ := api.Define[Row, Score](in, "score", `
def score(row):
    total = row["a"] * 2 + row["b"]
    return {"id": row["id"], "score": total, "ok": total > 10}
`)

for _, row := range rows {
    out, err := score(row)   // Score
}
```

`api.Define` and `api.Bind` return a plain `func(In) (Out, error)`, so the
Python function ends up looking like any other Go function. Underneath,
`host.Define`/`host.Func` return the untyped handle if you want `any` back.

`Define` and `Func` resolve the name once, which lifts the qstr interning and
the global lookup out of the call. `Instance.Call(name, ...)` does the lookup
every time and is only for one-offs.

The rest of the surface:

```go
out, _ := in.Exec(`print("hello")`)          // captured stdout: "hello\n"
v, _ := in.Eval("{'a': [1, 2], 'b': (3,)}")  // map[string]any{...}
in.Set("rows", []int{1, 2, 3})               // bind a global
```

A Python exception comes back as `*host.Error` carrying the traceback; the
instance stays usable afterwards.

An `Instance` is **serial-use only** — it owns one interpreter with one heap.
For concurrency, give each goroutine its own, or pool them.

### Cost of a call

Measured on an M3 Pro, with a three-key dict in and a three-key dict out:

| | ns/op |
|---|---|
| `fn()`, no arguments, returns `None` | 190 |
| `fn(20, 22)` returning an int | 302 |
| realistic call (dict in, dict out, a little arithmetic) | ~2040 |

Roughly half a million calls a second. Arguments are packed into one buffer
rather than pushed one at a time, because each push paid for an `nlr_push`
whose `setjmp` forces every call inside it through an `invoke_*` trampoline;
batching them cut inbound marshalling in half.

### Passing values

An `mp_obj_t` is a tagged pointer into the GC heap, so the host cannot hold
one — it has no way to keep it rooted across a collection. Something else has
to cross, and the two directions do it differently for reasons that are worth
stating.

**Out**, `wasm_value.c` walks the result and streams it through the imported
`val_*` callbacks, and the host rebuilds it natively. That is cheaper than
serialising — there is no encode and no parse — and it keeps the distinctions
a JSON round trip would lose: `int` versus `float`, `bytes`, `tuple` versus
`list`. Containers announce their length and are followed by exactly that many
values, so the host reassembles the tree with a small explicit stack.

| Python | Go |
|---|---|
| `None` | `nil` |
| `bool` | `bool` |
| `int` | `int64` |
| `float` | `float64` |
| `str` | `string` |
| `bytes` | `[]byte` |
| `list` | `[]any` |
| `tuple` | `host.Tuple` |
| `dict` | `map[string]any`, or `map[any]any` for non-string keys |
| anything else | `host.Object{Type, Repr}` |

**In**, `wasm_build.c` decodes a flat prefix walk of the value, one byte of tag
each, arriving in a single buffer. Values are assembled on a Python list
registered as a GC root, so a half-built container cannot be freed underneath
the decoder, and Go never holds an `mp_obj_t`.

Rendering Go values as Python source and letting the module parse them would
have been the obvious alternative, and it is worse on every axis: it puts
quoting, escaping and float round-tripping in the host, it turns every
argument into a parse, and a bug in the rendering becomes code injection.

Encoding the input is serialisation, which rendering Python source also was —
but a fixed binary format has no quoting, no parse ambiguity and no path from
data to code. An earlier version pushed one value per host call instead, which
avoided encoding entirely; it was slower, because each push paid for an
`nlr_push` whose `setjmp` forces every call inside it through an `invoke_*`
trampoline.

Why not a tagged-union value tree, as `wasmtime/component/val.h` does? That
shape suits a C ABI, where the embedder cannot cheaply be called back into, so
one call has to carry a whole value. Here the opposite is true: wasm2go turns
imports into ordinary Go method calls, so a callback costs about what a
function call costs, while a struct tree would mean laying out C structs into
linear memory from Go and keeping the offsets in sync by hand.

Why not JSON, which would certainly be less code? Because on this workload it
is slower than the entire call it would be part of:

| | ns/op |
|---|---|
| packing the argument dict | 133 |
| `json.Marshal` of the same dict | 948 |
| `json.Marshal` + `json.Unmarshal` | 2271 |

That is the Go side alone, before MicroPython's `json.loads` does any work, and
against a whole call that currently costs ~2000ns. JSON would also flatten
`int` and `float` into one numeric type, lose `bytes`, make `tuple`
indistinguishable from `list`, and restrict dict keys to strings.

## Why setjmp is the whole problem

MicroPython raises Python exceptions with a non-local return: `nlr_push()`
saves a resume point, and `nlr_jump()` unwinds the C stack back to the nearest
one. On every other target this is a few lines of assembly. Wasm has no way to
unwind or restore its own call stack, so `MICROPY_NLR_SETJMP` is the only
option — and Wasm has no native `setjmp` either.

There are three ways out, and the choice drives everything else in this port:

| | needs Wasm EH | host imports | nested handlers |
|---|---|---|---|
| wasi-libc `-lsetjmp` | **yes** | none | yes |
| `setjmp` returns 0, `longjmp` traps | no | 1 | **no — outermost only** |
| Emscripten SjLj lowering *(used here)* | no | ~11 | yes |

The first is what wasi-libc's `<setjmp.h>` gives you (`-mllvm
-wasm-enable-sjlj -lsetjmp`). It is by far the simplest, and if an
EH-capable engine is acceptable it is the better choice — but as of writing
V8/Node handle it while wazero, wasmer 5, and wasm2go do not.

The second is the trick used when a library has exactly one outermost
`try`/`catch` per call into the module: make `setjmp` always return 0 and
`longjmp` panic out to the host, which restores `__stack_pointer` and calls a
recovery export. MicroPython cannot use it. `nlr_push`/`nlr_pop` pairs nest
several deep in normal operation, and every Python `try`/`except` depends on
landing at the *innermost* matching handler — a scheme that only ever unwinds
to the outermost one would break exception handling outright.

So this port uses the third: LLVM's Emscripten-style SjLj lowering
(`-mllvm -enable-emscripten-sjlj`), which is a backend pass and not tied to
the Emscripten toolchain. It gives real, correctly nested `setjmp`/`longjmp`
with no exception handling instructions in the module at all.

### How it works

LLVM rewrites every call made from a function containing a `setjmp` into a call
to an imported `invoke_<signature>` trampoline. Then:

1. the host calls the real target through the exported indirect function
   table, inside its own `try`/`catch` (a `recover()` in Go);
2. `longjmp` reaches `_emscripten_throw_longjmp`, an import, which makes the
   host unwind its own frames;
3. the host catches that, rewinds `__stack_pointer` to where the invoke
   started, and calls the module's exported `setThrew()`;
4. back in the module, LLVM's generated code sees `__THREW__` set and either
   dispatches to the matching `setjmp` label, or rethrows so the next
   `invoke_*` further out can look — which is exactly what makes nesting work.

`wasm_sjlj.c` supplies the module half (`__wasm_setjmp`, `__wasm_setjmp_test`,
`setThrew`, `emscripten_longjmp`); `setjmp.h` shadows wasi-libc's, which
refuses to compile without EH. `internal/env` is the host half.

`WASM_FEATURES` in the Makefile pins the module to `mutable-globals`,
`multivalue`, `nontrapping-fptoint`, `sign-ext`, `reference-types`,
`bulk-memory` and `extended-const` — the set `ncruces/go-sqlite3-wasm` builds
with, minus tail calls, and with no `exception-handling`. wazero and wasmer
both compile the module; they fail only on the missing `env` imports.

### The Go side

    $ make wasm2go   # translate out/micropython.wasm to Go
    $ make test      # go test ./...

`internal/env` implements the `invoke_*` trampolines against the table and
`__stack_pointer` that wasm2go exposes. `internal/host` owns the module:
`ABI` does the crossing (the `val_*` callbacks outbound, argument encoding
inbound) and `Instance` is the API on top of it. `internal/api` is a generic
wrapper over that.

The generated code reads `m.___stack_pointer` on entry to each function and
writes it back on the normal return path only, so the trampoline restoring that
field is exactly what reclaims the abandoned frames' shadow stack. Note that it
must save the *value*: `X__stack_pointer()` returns a pointer to the live
field, so holding that pointer and writing it back later is a no-op, and the
shadow stack is never rewound — which shows up much later as a spurious
"maximum recursion depth exceeded".

Dropping WASI entirely is what makes the `libc/` tree in this directory
(generated by wasm2go's `libc-gen`) a realistic next step: set `WASI_TARGET` to
`wasm32` and add `-ffreestanding -nostdlib`, following the shape
`go-sqlite3-wasm` uses. `setjmp.h` and `wasm_sjlj.c` here replace
`libc/setjmp.h`, which `libc-gen` leaves empty — SQLite reports errors by
return code and never needs `setjmp`, which is why that gap does not show up
there.

## Known limitations

- **Recursion depth is bounded by the host's stack, not by
  `WASM_STACK_SIZE`.** Because every Python-level call passes through an
  `invoke_*` trampoline, each frame also costs a host frame. A Python frame
  uses ~274 bytes of shadow stack once pointers are spilled, and
  `MICROPY_C_STACK_SIZE` defaults to 96k, giving a recursion depth of about
  359. Stock `node` cannot reach that — each Python frame also costs a JS frame
  in the trampoline — so the `run` and `test` targets pass
  `--stack-size=8000`. Go needs no such flag; its goroutine stacks grow on
  demand. `make clean` is required after changing this, since the build does
  not track CFLAGS.

- **`gc_collect()` can only scan the shadow stack, so `wasm-opt
  --spill-pointers` is mandatory.** Wasm locals that are never address-taken
  live in the engine's value stack, which linear memory cannot see, so the GC
  would collect objects that are only referenced from such a local. This is not
  theoretical: without the spill pass, a loop doing 20k deep raises dies with a
  bogus `NotImplementedError` and then a null indirect call, in both the Node
  and Go hosts. `--spill-pointers` forces pointer-typed locals into the shadow
  stack; it costs ~10% size (517k -> 569k) and raises the per-frame stack cost
  from ~112 to ~274 bytes. `SPILL_POINTERS=0` disables it, for measuring the
  difference only.

- **The `invoke_*` indirection is not free.** Every call out of a function
  containing an `nlr_push` — which includes the bytecode dispatch loop — is now
  an indirect call through the host.

- Built at `MICROPY_CONFIG_ROM_LEVEL_MINIMUM`, so there is no `open`, no VFS,
  and a small builtin set. `import` does read from the host's preopened
  directories, via `mp_lexer_new_from_file()` in `main.c`. Raise the ROM level
  in `mpconfigport.h` for a fuller build.

- **No filesystem, no stdio, no REPL.** `import` cannot reach a real file,
  `open` does not exist, and `print` goes to the capture buffer the host reads
  after a call. Define what you need with `Exec` instead. This is what took the
  module from 594k to 374k, its exports from nineteen to eleven, and its WASI
  imports from thirteen to none.

- **An `Instance` is serial-use only.** It owns one interpreter and one heap.
  For concurrency, give each goroutine its own.

- **Structs cross via JSON, everything else does not.** Scalars, lists, tuples,
  dicts and bytes take the fast path in both directions; a Go struct is
  marshalled through `encoding/json` on the way in and out, which is the
  convenience path and costs accordingly. Prefer `map[string]any` when it
  matters.

[wasi-sdk]: https://github.com/WebAssembly/wasi-sdk
[Binaryen]: https://github.com/WebAssembly/binaryen
