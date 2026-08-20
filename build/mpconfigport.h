#include <stdint.h>
#include <alloca.h>

// options to control how MicroPython is built

// Use the minimal starting configuration (disables all optional features).
// Raise this to _CORE_FEATURES or _EVERYTHING for a fuller build.
#define MICROPY_CONFIG_ROM_LEVEL (MICROPY_CONFIG_ROM_LEVEL_MINIMUM)

// Without this `1 / 2` is a TypeError, which is a confusing thing to meet in
// the REPL.  Wasm has hardware doubles, so this is nearly free.
#define MICROPY_FLOAT_IMPL          (MICROPY_FLOAT_IMPL_DOUBLE)

// Wasm has no way to save and restore its own call stack, so none of the
// architecture specific nlr implementations apply.  Use the setjmp/longjmp one
// instead; setjmp.h and wasm_sjlj.c in this directory supply a setjmp that
// works without the Wasm exception handling proposal.
#define MICROPY_NLR_SETJMP          (1)

#define MICROPY_ENABLE_COMPILER     (1)
#define MICROPY_ENABLE_GC           (1)
#define MICROPY_HELPER_REPL         (0)
#define MICROPY_ENABLE_EXTERNAL_IMPORT (0)
#define MICROPY_PY_GC               (1)

// Deep recursion has to be caught in software: overflowing the shadow stack
// corrupts memory silently, and long before that the host runs out of its own
// stack inside the invoke_* trampolines.  See MICROPY_C_STACK_SIZE in the
// Makefile for how the budget is chosen.
#define MICROPY_STACK_CHECK         (1)

// print() would otherwise go through mp_hal_stdout_tx_strn_cooked, which
// translates LF to CRLF for a terminal.  There is no terminal here, and the
// host wants the bytes the program actually wrote.
#define MP_PLAT_PRINT_STRN(str, len) mp_hal_stdout_tx_strn((str), (len))

#define MICROPY_ALLOC_PATH_MAX      (256)

// Disable all optional sys module features.
#define MICROPY_PY_SYS_MODULES      (0)
#define MICROPY_PY_SYS_EXIT         (0)
#define MICROPY_PY_SYS_PATH         (0)
#define MICROPY_PY_SYS_ARGV         (0)

// type definitions for the specific machine

typedef long mp_off_t;

#define MICROPY_HW_BOARD_NAME "wasi"
#define MICROPY_HW_MCU_NAME "wasm32"

#define MP_STATE_PORT MP_STATE_VM

// Handy when retuning MICROPY_C_STACK_SIZE: micropython.stack_use() reports
// shadow stack bytes consumed, so you can measure the cost of a Python frame.
// #define MICROPY_PY_MICROPYTHON            (1)
// #define MICROPY_PY_MICROPYTHON_STACK_USE  (1)
