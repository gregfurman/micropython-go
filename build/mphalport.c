// The hooks MicroPython expects a port to supply. Nothing here is part of the
// host ABI; the interpreter calls into these on its own.

#include "host.h"
#include "py/builtin.h"
#include "py/lexer.h"
#include "py/mperrno.h"
#include "py/mphal.h"
#include "py/runtime.h"

// Called from the VM hook. The host raises KeyboardInterrupt inside the guest
// by answering this, which is how a cancelled context stops a running script.
void minimal_vm_poll(void) {
    if (host_poll()) {
        mp_raise_type(&mp_type_KeyboardInterrupt);
    }
}

mp_uint_t mp_hal_stdout_tx_strn(const char* str, size_t len) {
    host_stdout((uint32_t)(uintptr_t)str, (uint32_t)len);
    return len;
}

// There is no filesystem: source only ever arrives through eval and exec.
mp_lexer_t* mp_lexer_new_from_file(qstr filename) { mp_raise_OSError(MP_ENOENT); }

mp_import_stat_t mp_import_stat(const char* path) {
    (void)path;
    return MP_IMPORT_STAT_NO_EXIST;
}

mp_obj_t mp_builtin_open(size_t n_args, const mp_obj_t* args, mp_map_t* kwargs) {
    (void)n_args;
    (void)args;
    (void)kwargs;
    mp_raise_OSError(MP_ENOENT);
}
MP_DEFINE_CONST_FUN_OBJ_KW(mp_builtin_open_obj, 1, mp_builtin_open);

void MP_NORETURN __fatal_error(const char* msg) {
    (void)msg;
    __builtin_trap();
}
