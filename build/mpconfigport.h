#include <stdint.h>
#include <alloca.h>

#define MICROPY_CONFIG_ROM_LEVEL (MICROPY_CONFIG_ROM_LEVEL_MINIMUM)
#define MICROPY_ENABLE_GC (1)
#define MICROPY_ENABLE_COMPILER     (1)
#define MICROPY_STACK_CHECK (1)
#define MICROPY_C_STACK_SIZE (98304)

#include "port/mpconfigport_common.h"

#undef MICROPY_LONGINT_IMPL
#define MICROPY_LONGINT_IMPL (MICROPY_LONGINT_IMPL_MPZ)

#undef MICROPY_FLOAT_IMPL
#define MICROPY_FLOAT_IMPL (MICROPY_FLOAT_IMPL_DOUBLE)

#define MICROPY_USE_INTERNAL_ERRNO (1)
#define MICROPY_USE_INTERNAL_PRINTF (0)
#define MICROPY_PY_SYS_PLATFORM "wasi"

#define MICROPY_PY_GC (1)
#define MICROPY_BUILTIN_METHOD_CHECK_SELF_ARG (1)
#define MICROPY_FULL_CHECKS (1)
#define MICROPY_WARNINGS (1)
#define MICROPY_PY_STR_BYTES_CMP_WARN (1)

#define MICROPY_PY_FSTRINGS (1)
#define MICROPY_CPYTHON_COMPAT (1)
#define MICROPY_PY_TSTRINGS (1)
#define MICROPY_ENABLE_SOURCE_LINE (1)
#define MICROPY_PY_BUILTINS_SET (1)
#define MICROPY_PY_BUILTINS_FROZENSET (1)
#define MICROPY_PY_BUILTINS_SLICE (1)
#define MICROPY_PY_BUILTINS_SLICE_ATTRS (1)
#define MICROPY_PY_BUILTINS_SLICE_INDICES (1)
#define MICROPY_PY_BUILTINS_BYTEARRAY (1)
#define MICROPY_PY_BUILTINS_MEMORYVIEW (1)
#define MICROPY_PY_BUILTINS_COMPILE (1)
#define MICROPY_PY_BUILTINS_DICT_FROMKEYS (1)
#define MICROPY_PY_BUILTINS_RANGE_ATTRS (1)
#define MICROPY_PY_ASSIGN_EXPR (1)
#define MICROPY_MULTIPLE_INHERITANCE (1)
#define MICROPY_PY_BUILTINS_REVERSED (1)
#define MICROPY_PY_BUILTINS_STR_OP_MODULO (1)
#define MICROPY_PY_REVERSE_SPECIAL_METHODS (1)
#define MICROPY_PY_ALL_SPECIAL_METHODS (1)

#define MICROPY_PY_JSON (1)
#define MICROPY_PY_MATH (1)
#define MICROPY_PY_IO (1)
#define MICROPY_PY_MICROPYTHON (1)
#define MICROPY_PY_ARRAY (1)
#define MICROPY_PY_COLLECTIONS (1)
#define MICROPY_PY_STRUCT (1)
#define MICROPY_PY_SYS_MAXSIZE (1)
#define MICROPY_PY_ATTRTUPLE (1)
#define MICROPY_PY_RE (1)
#define MICROPY_PY_RE_SUB (1)
#define MICROPY_PY_RE_MATCH_GROUPS (1)
#define MICROPY_PY_RE_MATCH_SPAN_START_END (1)

// Otherwise print() goes through mp_hal_stdout_tx_strn_cooked, which turns LF
// into CRLF for a terminal. There is no terminal, and the host wants the bytes
// the program actually wrote.
#define MP_PLAT_PRINT_STRN(str, len) mp_hal_stdout_tx_strn((str), (len))

#define MICROPY_VM_HOOK_COUNT (256)
#define MICROPY_VM_HOOK_INIT static uint16_t vm_hook_divisor = MICROPY_VM_HOOK_COUNT;
#define MICROPY_VM_HOOK_POLL                        \
    if (--vm_hook_divisor == 0) {                   \
        vm_hook_divisor = MICROPY_VM_HOOK_COUNT;    \
        extern void minimal_vm_poll(void);          \
        minimal_vm_poll();                          \
    }
#define MICROPY_VM_HOOK_LOOP MICROPY_VM_HOOK_POLL
#define MICROPY_VM_HOOK_RETURN MICROPY_VM_HOOK_POLL
