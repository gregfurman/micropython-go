# Support matrix

What micropython-go's interpreter supports, against upstream MicroPython.

The MicroPython column is v1.28.0's unix port (`standard` variant,
`MICROPY_CONFIG_ROM_LEVEL_EVERYTHING`) built from the same commit this repo vendors, so the
comparison isolates configuration rather than version drift. micropython-go is built at
`MICROPY_CONFIG_ROM_LEVEL_MINIMUM` plus roughly 40 explicit flags; see
[`build/mpconfigport.h`](build/mpconfigport.h).

Every row was produced by running the expression in both interpreters and recording whether it
raised, not by reading config flags. A name appearing in `dir(builtins)` does not guarantee it
works.

|     |                                                       |
| :-: | ----------------------------------------------------- |
|  ✓  | works in micropython-go                               |
|  ✗  | missing in micropython-go, present in MicroPython     |
|  -  | missing in both; not something micropython-go dropped |

## Modules

Imports resolve against built-in modules only. There is no filesystem behind `import`, so nothing
outside this table is reachable.

| Module               | micropython-go | MicroPython | Note                                                   |
| -------------------- | :------------: | :---------: | ------------------------------------------------------ |
| `array`              |       ✓        |      ✓      |                                                        |
| `collections`        |       ✓        |      ✓      |                                                        |
| `gc`                 |       ✓        |      ✓      |                                                        |
| `io`                 |       ✓        |      ✓      |                                                        |
| `json`               |       ✓        |      ✓      |                                                        |
| `math`               |       ✓        |      ✓      |                                                        |
| `micropython`        |       ✓        |      ✓      |                                                        |
| `re`                 |       ✓        |      ✓      |                                                        |
| `struct`             |       ✓        |      ✓      |                                                        |
| `sys`                |       ✓        |      ✓      |                                                        |
| `string.templatelib` |       ✓        |      ✗      | Side effect of t-strings, not the full `string` module |
| `asyncio`            |       ✗        |      ✓      | `async`/`await` is a SyntaxError in micropython-go     |
| `binascii`           |       ✗        |      ✓      |                                                        |
| `btree`              |       ✗        |      ✓      | unix-port binding                                      |
| `cmath`              |       ✗        |      ✓      |                                                        |
| `deflate`            |       ✗        |      ✓      |                                                        |
| `errno`              |       ✗        |      ✓      |                                                        |
| `hashlib`            |       ✗        |      ✓      |                                                        |
| `heapq`              |       ✗        |      ✓      |                                                        |
| `machine`            |       ✗        |      ✓      | hardware binding                                       |
| `os`                 |       ✗        |      ✓      | host binding                                           |
| `platform`           |       ✗        |      ✓      |                                                        |
| `random`             |       ✗        |      ✓      |                                                        |
| `select`             |       ✗        |      ✓      | host binding                                           |
| `socket`             |       ✗        |      ✓      | host binding                                           |
| `ssl`                |       ✗        |      ✓      | host binding                                           |
| `termios`            |       ✗        |      ✓      | unix-port binding                                      |
| `time`               |       ✗        |      ✓      |                                                        |
| `uctypes`            |       ✗        |      ✓      |                                                        |
| `vfs`                |       ✗        |      ✓      | filesystem binding                                     |
| `_thread`            |       ✗        |      ✓      | no threads in the sandbox                              |
| `abc`                |       -        |      -      | Not in MicroPython at any ROM level                    |
| `base64`             |       -        |      -      | Not in MicroPython at any ROM level                    |
| `copy`               |       -        |      -      | Not in MicroPython at any ROM level                    |
| `dataclasses`        |       -        |      -      | Not in MicroPython at any ROM level                    |
| `datetime`           |       -        |      -      | Not in MicroPython at any ROM level                    |
| `decimal`            |       -        |      -      | Not in MicroPython at any ROM level                    |
| `enum`               |       -        |      -      | Not in MicroPython at any ROM level                    |
| `functools`          |       -        |      -      | Not in MicroPython at any ROM level                    |
| `inspect`            |       -        |      -      | Not in MicroPython at any ROM level                    |
| `itertools`          |       -        |      -      | Not in MicroPython at any ROM level                    |
| `logging`            |       -        |      -      | Not in MicroPython at any ROM level                    |
| `operator`           |       -        |      -      | Not in MicroPython at any ROM level                    |
| `pickle`             |       -        |      -      | Not in MicroPython at any ROM level                    |
| `threading`          |       -        |      -      | Not in MicroPython at any ROM level                    |
| `traceback`          |       -        |      -      | Not in MicroPython at any ROM level                    |
| `typing`             |       -        |      -      | Not in MicroPython at any ROM level                    |
| `unittest`           |       -        |      -      | Not in MicroPython at any ROM level                    |
| `warnings`           |       -        |      -      | Not in MicroPython at any ROM level                    |
| `weakref`            |       -        |      -      | Not in MicroPython at any ROM level                    |

## Built-in functions

`ROM_LEVEL_MINIMUM` drops several built-ins that ordinary Python assumes are always present. These
are the most likely cause of a working script failing under micropython-go. Each missing name is a
one-line flag in `build/mpconfigport.h` if you want it back.

| Name             | micropython-go | MicroPython | Note                                                              |
| ---------------- | :------------: | :---------: | ----------------------------------------------------------------- |
| `property`       |       ✗        |      ✓      | `@property` fails at class-definition time; the worst of these    |
| `min`            |       ✗        |      ✓      | `NameError`; substitute `sorted(xs)[0]` or a fold                 |
| `max`            |       ✗        |      ✓      | `NameError`; substitute `sorted(xs)[-1]` or a fold                |
| `filter`         |       ✗        |      ✓      | A comprehension covers it                                         |
| `enumerate`      |       ✗        |      ✓      | Use `zip(range(len(xs)), xs)`                                     |
| `input`          |       ✗        |      ✓      | No stdin to read from regardless                                  |
| `NotImplemented` |       ✗        |      ✓      | Return `None` from unsupported operators                          |
| `format()`       |       -        |      -      | `str.format` and `%` both work; only the free function is missing |
| `vars()`         |       -        |      -      |                                                                   |
| `open`           |       ✓        |      ✓      | Present, but always raises `OSError`; there is no filesystem      |
| `slice`          |       -        |      -      | Name exists; `slice()` cannot be constructed in either build      |
| `abs`            |       ✓        |      ✓      |                                                                   |
| `all`            |       ✓        |      ✓      |                                                                   |
| `any`            |       ✓        |      ✓      |                                                                   |
| `bin`            |       ✓        |      ✓      |                                                                   |
| `bool`           |       ✓        |      ✓      |                                                                   |
| `bytearray`      |       ✓        |      ✓      |                                                                   |
| `bytes`          |       ✓        |      ✓      |                                                                   |
| `callable`       |       ✓        |      ✓      |                                                                   |
| `chr`            |       ✓        |      ✓      |                                                                   |
| `classmethod`    |       ✓        |      ✓      |                                                                   |
| `compile`        |       ✓        |      ✓      |                                                                   |
| `complex`        |       ✓        |      ✓      |                                                                   |
| `delattr`        |       ✓        |      ✓      |                                                                   |
| `dict`           |       ✓        |      ✓      |                                                                   |
| `dir`            |       ✓        |      ✓      |                                                                   |
| `divmod`         |       ✓        |      ✓      |                                                                   |
| `eval`           |       ✓        |      ✓      |                                                                   |
| `exec`           |       ✓        |      ✓      |                                                                   |
| `float`          |       ✓        |      ✓      |                                                                   |
| `frozenset`      |       ✓        |      ✓      |                                                                   |
| `getattr`        |       ✓        |      ✓      |                                                                   |
| `globals`        |       ✓        |      ✓      |                                                                   |
| `hasattr`        |       ✓        |      ✓      |                                                                   |
| `hash`           |       ✓        |      ✓      |                                                                   |
| `hex`            |       ✓        |      ✓      |                                                                   |
| `id`             |       ✓        |      ✓      |                                                                   |
| `int`            |       ✓        |      ✓      |                                                                   |
| `isinstance`     |       ✓        |      ✓      |                                                                   |
| `issubclass`     |       ✓        |      ✓      |                                                                   |
| `iter`           |       ✓        |      ✓      |                                                                   |
| `len`            |       ✓        |      ✓      |                                                                   |
| `list`           |       ✓        |      ✓      |                                                                   |
| `locals`         |       ✓        |      ✓      |                                                                   |
| `map`            |       ✓        |      ✓      |                                                                   |
| `memoryview`     |       ✓        |      ✓      |                                                                   |
| `next`           |       ✓        |      ✓      |                                                                   |
| `object`         |       ✓        |      ✓      |                                                                   |
| `oct`            |       ✓        |      ✓      |                                                                   |
| `ord`            |       ✓        |      ✓      |                                                                   |
| `pow`            |       ✓        |      ✓      |                                                                   |
| `print`          |       ✓        |      ✓      |                                                                   |
| `range`          |       ✓        |      ✓      |                                                                   |
| `repr`           |       ✓        |      ✓      |                                                                   |
| `reversed`       |       ✓        |      ✓      |                                                                   |
| `round`          |       ✓        |      ✓      |                                                                   |
| `set`            |       ✓        |      ✓      |                                                                   |
| `setattr`        |       ✓        |      ✓      |                                                                   |
| `sorted`         |       ✓        |      ✓      |                                                                   |
| `staticmethod`   |       ✓        |      ✓      |                                                                   |
| `str`            |       ✓        |      ✓      |                                                                   |
| `sum`            |       ✓        |      ✓      |                                                                   |
| `super`          |       ✓        |      ✓      |                                                                   |
| `tuple`          |       ✓        |      ✓      |                                                                   |
| `type`           |       ✓        |      ✓      |                                                                   |
| `zip`            |       ✓        |      ✓      |                                                                   |

## Language and syntax

The compiler is enabled with CPython compatibility on, so the syntax surface is close to complete.
Two exceptions, in opposite directions.

| Feature                         | micropython-go | MicroPython | Note                                                         |
| ------------------------------- | :------------: | :---------: | ------------------------------------------------------------ |
| `async def` / `await`           |       ✗        |      ✓      | SyntaxError at compile time; coroutines unavailable          |
| t-strings (PEP 750)             |       ✓        |      ✗      | `t'{x}'` yields a `Template`; MicroPython raises SyntaxError |
| `match` / `case`                |       -        |      -      | Structural pattern matching is not in MicroPython            |
| `slice()` constructor           |       -        |      -      | Slice _syntax_ `xs[1:3:2]` works normally in both            |
| f-strings                       |       ✓        |      ✓      |                                                              |
| `f'{x=}'`                       |       ✓        |      ✓      |                                                              |
| Walrus `:=`                     |       ✓        |      ✓      |                                                              |
| List comprehensions             |       ✓        |      ✓      |                                                              |
| Dict comprehensions             |       ✓        |      ✓      |                                                              |
| Set comprehensions              |       ✓        |      ✓      |                                                              |
| Generator expressions           |       ✓        |      ✓      |                                                              |
| Decorators                      |       ✓        |      ✓      |                                                              |
| `yield`                         |       ✓        |      ✓      |                                                              |
| `yield from`                    |       ✓        |      ✓      |                                                              |
| `generator.send()`              |       ✓        |      ✓      |                                                              |
| Classes                         |       ✓        |      ✓      |                                                              |
| Multiple inheritance            |       ✓        |      ✓      |                                                              |
| `super()`                       |       ✓        |      ✓      |                                                              |
| `__slots__`                     |       ✓        |      ✓      |                                                              |
| `@classmethod`                  |       ✓        |      ✓      |                                                              |
| `@staticmethod`                 |       ✓        |      ✓      |                                                              |
| Special methods                 |       ✓        |      ✓      |                                                              |
| Reverse special methods         |       ✓        |      ✓      |                                                              |
| `__getattr__`                   |       ✓        |      ✓      |                                                              |
| `__call__`                      |       ✓        |      ✓      |                                                              |
| Context managers / `with`       |       ✓        |      ✓      |                                                              |
| `try`/`except`/`else`/`finally` |       ✓        |      ✓      |                                                              |
| `raise ... from`                |       ✓        |      ✓      |                                                              |
| Custom exceptions               |       ✓        |      ✓      |                                                              |
| `namedtuple`                    |       ✓        |      ✓      |                                                              |
| Keyword-only args               |       ✓        |      ✓      |                                                              |
| `**kwargs`                      |       ✓        |      ✓      |                                                              |
| `f(*a, **k)`                    |       ✓        |      ✓      |                                                              |
| `a, *b = xs`                    |       ✓        |      ✓      |                                                              |
| `global` / `nonlocal`           |       ✓        |      ✓      |                                                              |
| `assert`                        |       ✓        |      ✓      |                                                              |
| `del`                           |       ✓        |      ✓      |                                                              |
| Function annotations            |       ✓        |      ✓      |                                                              |
| Variable annotations            |       ✓        |      ✓      |                                                              |
| Chained comparison              |       ✓        |      ✓      |                                                              |
| Conditional expressions         |       ✓        |      ✓      |                                                              |
| `lambda`                        |       ✓        |      ✓      |                                                              |

## Numbers, strings and bytes

| Feature                                       | micropython-go | MicroPython | Note                                               |
| --------------------------------------------- | :------------: | :---------: | -------------------------------------------------- |
| `int`                                         |       ✓        |      ✓      | Arbitrary precision via MPZ; `2**200` is exact     |
| `float`                                       |       ✓        |      ✓      | 64-bit double                                      |
| `complex`                                     |       ✓        |      ✓      | Type works; `cmath` is not built in micropython-go |
| `sys.maxsize`                                 |     2³¹−1      |    2⁶³−1    | wasm32, so the small-int range is 32-bit           |
| `bytes.hex()`                                 |       ✗        |      ✓      | `AttributeError`; `binascii` also unavailable      |
| `str.center` `str.rjust`                      |       ✗        |      ✓      | Padding helpers are not compiled in                |
| `str.format`, `%` operator, `encode`/`decode` |       ✓        |      ✓      |                                                    |
| `set` `frozenset` `bytearray` `memoryview`    |       ✓        |      ✓      | Including sliced memoryviews                       |

A `sys.maxsize` of 2³¹−1 is a small-integer boundary, not a ceiling: larger values promote to
arbitrary precision rather than overflowing.

## Sandbox behaviour

Not MicroPython features that were turned on or off. These exist because the interpreter runs
inside a Go process with no host access.

| Behaviour             | Detail                                                                                            |
| --------------------- | ------------------------------------------------------------------------------------------------- |
| No filesystem         | `open()` raises `OSError`; `import` cannot reach a real file even in the working directory        |
| `print()` is captured | Output is returned by `Exec`; the host's stdout receives nothing                                  |
| Host functions        | Go functions bind to Python names. Failures raise `HostError`, which subclasses `RuntimeError`    |
| Cancellation          | A VM hook polls every 256 instructions and raises `KeyboardInterrupt`                             |
| Recursion depth       | Roughly 340-385 frames (`MICROPY_C_STACK_SIZE` is 96 KiB); overflow is a catchable `RuntimeError` |
| Heap                  | Configurable via `WithHeapSize`; exhaustion raises `MemoryError` and the instance survives        |

## Reproducing

The MicroPython column was produced from the vendored submodule:

```bash
make -C micropython/ports/unix MICROPY_PY_FFI=0 -j8
./micropython/ports/unix/build-standard/micropython
```

`MICROPY_PY_FFI=0` avoids a libffi header dependency; `ffi` is a unix-port binding with no sandbox
equivalent and is excluded from the counts above.
