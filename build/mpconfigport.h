#include <stdint.h>
#include <alloca.h>

// options to control how MicroPython is built

// Use the minimal starting configuration (disables all optional features).
// Raise this to _CORE_FEATURES or _EVERYTHING for a fuller build.
#define MICROPY_CONFIG_ROM_LEVEL (MICROPY_CONFIG_ROM_LEVEL_MINIMUM)

// Go's int is 64-bit, so without arbitrary-precision integers any value beyond
// +/-2^30 fails to cross with "small int overflow".
#define MICROPY_LONGINT_IMPL        (MICROPY_LONGINT_IMPL_MPZ)

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

// Nothing can interrupt running Python from outside: no scheduler, no signals,
// no second thread. So the VM asks the host, every so often, whether it should
// stop. Without this a guest loop wedges the host permanently.
#define MICROPY_VM_HOOK_COUNT (256)
#define MICROPY_VM_HOOK_INIT static uint16_t vm_hook_divisor = MICROPY_VM_HOOK_COUNT;
#define MICROPY_VM_HOOK_POLL                        \
    if (--vm_hook_divisor == 0) {                   \
        vm_hook_divisor = MICROPY_VM_HOOK_COUNT;    \
        extern void mp_api_vm_poll(void);           \
        mp_api_vm_poll();                           \
    }
#define MICROPY_VM_HOOK_LOOP MICROPY_VM_HOOK_POLL
#define MICROPY_VM_HOOK_RETURN MICROPY_VM_HOOK_POLL

// f-strings, and __name__ on a type. Both are things a Python author reaches
// for without thinking -- and without CPYTHON_COMPAT, type(e).__name__ inside
// an except block raises AttributeError, which is a baffling way to find out
// the build is trimmed.
#define MICROPY_PY_FSTRINGS         (1)
#define MICROPY_CPYTHON_COMPAT      (1)

// Without this the compiler emits no line table, so every traceback frame the
// host receives reports line 0 -- which is most of the value of having a
// structured traceback at all.  The cost is a few bytes per bytecode chunk.
#define MICROPY_ENABLE_SOURCE_LINE  (1)

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
