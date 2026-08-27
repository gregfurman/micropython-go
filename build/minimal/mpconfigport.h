#include <stdint.h>
#include <alloca.h>


#define MICROPY_ENABLE_GC (1)
#define MICROPY_GCREGS_SETJMP (1)
#define MICROPY_ENABLE_COMPILER     (1)

#include "port/mpconfigport_common.h"

#undef MICROPY_LONGINT_IMPL
#define MICROPY_LONGINT_IMPL (MICROPY_LONGINT_IMPL_MPZ)

#undef MICROPY_FLOAT_IMPL
#define MICROPY_FLOAT_IMPL (MICROPY_FLOAT_IMPL_DOUBLE)

#define MICROPY_USE_INTERNAL_ERRNO (1)
#define MICROPY_USE_INTERNAL_PRINTF (0)
#define MICROPY_PY_SYS_PLATFORM "wasi"

// Otherwise print() goes through mp_hal_stdout_tx_strn_cooked, which turns LF
// into CRLF for a terminal. There is no terminal, and the host wants the bytes
// the program actually wrote.
#define MP_PLAT_PRINT_STRN(str, len) mp_hal_stdout_tx_strn((str), (len))

#define MICROPY_VM_HOOK_LOOP
#define MICROPY_VM_HOOK_RETURN
