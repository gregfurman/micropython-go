#include <stdint.h>
#include <alloca.h>

// Start from nothing and turn on what this port needs, rather than trimming
// down from a fuller level.
#define MICROPY_CONFIG_ROM_LEVEL (MICROPY_CONFIG_ROM_LEVEL_MINIMUM)

// --- the port itself -------------------------------------------------------

// Go's int is 64-bit, so without arbitrary precision any value beyond +/-2^30
// fails to cross with "small int overflow".
#define MICROPY_LONGINT_IMPL        (MICROPY_LONGINT_IMPL_MPZ)

// Without this `1 / 2` is a TypeError. Wasm has hardware doubles, so it is
// nearly free.
#define MICROPY_FLOAT_IMPL          (MICROPY_FLOAT_IMPL_DOUBLE)

// Wasm cannot save and restore its own call stack, so none of the
// architecture-specific nlr implementations apply. setjmp.h and wasm_sjlj.c in
// this directory supply a setjmp that needs no exception handling.
#define MICROPY_NLR_SETJMP          (1)

// Deep recursion has to be caught in software: overflowing the shadow stack
// corrupts memory silently, and the host runs out of its own stack inside the
// invoke_* trampolines long before that. See MICROPY_C_STACK_SIZE.
#define MICROPY_STACK_CHECK         (1)

// Otherwise print() goes through mp_hal_stdout_tx_strn_cooked, which turns LF
// into CRLF for a terminal. There is no terminal, and the host wants the bytes
// the program actually wrote.
#define MP_PLAT_PRINT_STRN(str, len) mp_hal_stdout_tx_strn((str), (len))

// Nothing can interrupt running Python from outside: no scheduler, no signals,
// no second thread. So the VM asks the host, every so often, whether to stop.
// Without this a guest loop wedges the host permanently.
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

#define MICROPY_ENABLE_COMPILER     (1)
#define MICROPY_ENABLE_GC           (1)
#define MICROPY_PY_GC               (1)
#define MICROPY_HELPER_REPL         (0)
#define MICROPY_ENABLE_EXTERNAL_IMPORT (0)
#define MICROPY_ALLOC_PATH_MAX      (256)

// --- safety ----------------------------------------------------------------

// list.append(1, 2) otherwise uses the int as a list. MicroPython's own note:
// "undefined behaviour (usually segfault)". Guest-reachable in one line.
#define MICROPY_BUILTIN_METHOD_CHECK_SELF_ARG (1)

// Without these, bytes([-1]) and bytes([256]) quietly produce a wrong byte
// rather than raising ValueError.
#define MICROPY_FULL_CHECKS (1)

// b"123" == "123" is False in a way that is almost always a bug.
#define MICROPY_WARNINGS (1)
#define MICROPY_PY_STR_BYTES_CMP_WARN (1)

// --- language --------------------------------------------------------------

// Without CPYTHON_COMPAT, type(e).__name__ inside an except block raises
// AttributeError -- a baffling way to find out the build is trimmed.
#define MICROPY_PY_FSTRINGS         (1)
#define MICROPY_CPYTHON_COMPAT      (1)
#define MICROPY_PY_TSTRINGS         (1)

// Without a line table every traceback frame the host receives reports line 0.
#define MICROPY_ENABLE_SOURCE_LINE  (1)

#define MICROPY_PY_BUILTINS_SET       (1)
#define MICROPY_PY_BUILTINS_FROZENSET (1)

// Without SLICE, a[1:2] is a SyntaxError, which points at the wrong thing
// entirely. _ATTRS and _INDICES make a slice readable (s.start, s.indices(n))
// rather than only usable in brackets.
#define MICROPY_PY_BUILTINS_SLICE         (1)
#define MICROPY_PY_BUILTINS_SLICE_ATTRS   (1)
#define MICROPY_PY_BUILTINS_SLICE_INDICES (1)

#define MICROPY_PY_BUILTINS_BYTEARRAY     (1)
#define MICROPY_PY_BUILTINS_DICT_FROMKEYS (1)
#define MICROPY_PY_BUILTINS_RANGE_ATTRS   (1)
#define MICROPY_PY_ASSIGN_EXPR            (1)
#define MICROPY_MULTIPLE_INHERITANCE      (1)

// MicroPython implements stepped slices for list only, so 'abc'[::-1] raises
// NotImplementedError however this is configured; reversed() is the way.
#define MICROPY_PY_BUILTINS_REVERSED (1)

// str.format is always compiled in, so without _STR_OP_MODULO the two halves
// of Python's formatting disagree about whether they exist.
#define MICROPY_PY_BUILTINS_STR_OP_MODULO (1)

// Without these a class can only be the left operand: 2 * vec works and
// vec * 2 does not, surfacing as "unsupported type for operator" from code
// that looks correct. ALL extends it past the arithmetic operators.
#define MICROPY_PY_REVERSE_SPECIAL_METHODS (1)
#define MICROPY_PY_ALL_SPECIAL_METHODS     (1)

// --- modules ---------------------------------------------------------------

// Parsing a request body and matching a pattern are the bread and butter of
// the use case this port exists for. See SRC_C in the Makefile.
#define MICROPY_PY_MATH                  (1)
#define MICROPY_PY_BUILTINS_COMPILE         (1)
#define MICROPY_PY_JSON (1)
#define MICROPY_PY_RE   (1)
#define MICROPY_PY_RE_SUB                  (1)
#define MICROPY_PY_RE_MATCH_GROUPS         (1)
#define MICROPY_PY_RE_MATCH_SPAN_START_END (1)

// Not optional alongside json: json.loads reads through a StringIO, so
// mp_type_stringio has to exist or modjson.c does not link. It also brings in
// open(), which main.c stubs out.
#define MICROPY_PY_IO   (1)

// sys.implementation and sys.maxsize are how portable Python asks what it is
// running on. path and argv would only ever be empty: external import is off,
// and there is no command line.
#define MICROPY_PY_SYS_MAXSIZE      (1)
#define MICROPY_PY_ATTRTUPLE        (1)
#define MICROPY_PY_SYS_MODULES      (0)
#define MICROPY_PY_SYS_EXIT         (0)
#define MICROPY_PY_SYS_PATH         (0)
#define MICROPY_PY_SYS_ARGV         (0)

// --- machine ---------------------------------------------------------------

typedef long mp_off_t;

#define MICROPY_HW_BOARD_NAME "wasi"
#define MICROPY_HW_MCU_NAME "wasm32"

#define MP_STATE_PORT MP_STATE_VM
