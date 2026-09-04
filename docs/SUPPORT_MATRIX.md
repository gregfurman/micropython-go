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
| `time`               |       ✓        |      ✓      | Only `sleep*` and `ticks*`; no `time()` or `gmtime()`  |
| `string.templatelib` |       ✓        |      ✗      | Side effect of t-strings, not the full `string` module |
| `asyncio`            |       ✗        |      ✓      | `async`/`await` is a SyntaxError in micropython-go     |
| `binascii`           |       ✗        |      ✓      |                                                        |
| `btree`              |       ✗        |      ✓      | unix-port binding                                      |
| `cmath`              |       ✗        |      ✓      |                                                        |
| `deflate`            |       ✗        |      ✓      |                                                        |
| `errno`              |       ✓        |      ✓      |                                                        |
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
| `weakref`            |       ✓        |      ✓      | Needs MICROPY_ENABLE_FINALISER                         |

## Built-in functions

`ROM_LEVEL_MINIMUM` drops several built-ins that ordinary Python assumes are always present. These
are the most likely cause of a working script failing under micropython-go. Each missing name is a
one-line flag in `build/mpconfigport.h` if you want it back.

| Name             | micropython-go | MicroPython | Note                                                              |
| ---------------- | :------------: | :---------: | ----------------------------------------------------------------- |
| `property`       |       ✓        |      ✓      |                                                                   |
| `min`            |       ✗        |      ✓      | `NameError`; substitute `sorted(xs)[0]` or a fold                 |
| `max`            |       ✗        |      ✓      | `NameError`; substitute `sorted(xs)[-1]` or a fold                |
| `filter`         |       ✓        |      ✓      |                                                                   |
| `enumerate`      |       ✗        |      ✓      | Use `zip(range(len(xs)), xs)`                                     |
| `input`          |       ✗        |      ✓      | No stdin to read from regardless                                  |
| `NotImplemented` |       ✓        |      ✓      |                                                                   |
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
| `bytes.hex()`                                 |       ✓        |      ✓      |                                                    |
| `str.center`                                  |       ✓        |      ✓      |                                                    |
| `str.rjust` `str.ljust`                       |       ✗        |      ✓      | Only center is compiled in                         |
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
| `print()` is captured | Output goes to the WithStdout writer; the host's stdout receives nothing                          |
| Host functions        | Go functions bind to Python names. Failures raise `HostError`, which subclasses `RuntimeError`    |
| Cancellation          | A VM hook polls every 256 instructions and raises `KeyboardInterrupt`                             |
| Recursion depth       | Roughly 340-385 frames (`MICROPY_C_STACK_SIZE` is 96 KiB); overflow is a catchable `RuntimeError` |
| Heap                  | Configurable via `WithHeapSize`; exhaustion raises `MemoryError` and the instance survives        |

## Skipped upstream tests

<!-- BEGIN generated: skipped upstream tests -->

45 upstream tests are skipped. Each is a feature this port does not
compile in, or a difference in how the reference reports something, rather than
a defect in the interpreter.

| Reason | Tests |
|---|---|
| an exception in a weakref callback prints a traceback here rather than being swallowed | `weakref_callback_exception.py` |
| async/await is not compiled in | `async_await.py` `async_await2.py` `async_def.py` `async_for.py` `async_for2.py` `async_with.py` `async_with2.py` `async_with_break.py` `async_with_return.py` |
| help() lists the modules a build has, so the text is config-specific | `builtin_help.py` |
| import * from a class honours __all__ differently here | `import_star_nonmodule.py` |
| import string.templatelib fails; only the attribute resolves | `string_module_tstring.py` `string_tstring_basic.py` `string_tstring_constructor.py` `string_tstring_constructor1.py` `string_tstring_errors1.py` `string_tstring_operations.py` `string_tstring_parser1.py` |
| needs MICROPY_PY_MATH_CONSTANTS | `math_constants_extra.py` |
| needs MICROPY_PY_MATH_FACTORIAL | `math_factorial_intbig.py` |
| needs MICROPY_PY_MATH_ISCLOSE | `math_isclose.py` |
| needs MICROPY_PY_MATH_SPECIAL_FUNCTIONS | `math_domain_special.py` `math_fun_special.py` |
| needs __code__.co_lines, beyond MICROPY_PY_FUNCTION_ATTRS_CODE | `fun_code_colines.py` |
| needs bytearray slice assignment | `bytearray_slice_assign.py` |
| needs cmath | `cmath_dunder.py` `cmath_fun.py` `cmath_fun_special.py` |
| needs machine, a hardware binding | `subclass_native_call.py` |
| needs os, which has no meaning without a filesystem | `attrtuple2.py` |
| needs print(file=...), which still raises TypeError with STDFILES on | `recursive_data.py` `sys_stdio.py` `sys_stdio_buffer.py` |
| needs random | `class_setname_hazard_rand.py` `float_format_accuracy.py` |
| needs sys.path and __file__, which this build deliberately omits | `sys_path.py` |
| needs the full __code__ attribute set | `fun_code_full.py` |
| needs uctypes | `memoryview_slice_assign.py` `memoryview_slice_size.py` |
| only meaningful in a nan-boxing build | `nanbox_smallint.py` |
| subclassing io.IOBase raises TypeError: argument num/types mismatch | `io_iobase.py` |
| the reference strips filenames from tracebacks; this build reports <string> | `sys_tracebacklimit.py` |
| tunes itself on sys.implementation._mpy, which needs persistent code support | `bytecode_limit.py` |
| uses t"\8", an escape MicroPython rejects and CPython only warns about | `string_tstring_basic1.py` |

<!-- END generated: skipped upstream tests -->

## Reproducing

The MicroPython column was produced from the vendored submodule:

```bash
make -C micropython/ports/unix MICROPY_PY_FFI=0 -j8
./micropython/ports/unix/build-standard/micropython
```

`MICROPY_PY_FFI=0` avoids a libffi header dependency; `ffi` is a unix-port binding with no sandbox
equivalent and is excluded from the counts above.
