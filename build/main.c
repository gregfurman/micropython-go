/*
 * Port runtime: the GC's stack scan, the one output sink, and the two paths
 * that should never be taken.
 *
 * There is no main().  The module is a reactor, driven by the host through the
 * exports in wasm_api.c.
 */

#include "py/builtin.h"
#include "py/mperrno.h"
#include "py/gc.h"
#include "py/mphal.h"
#include "py/runtime.h"

#include "wasm_api.h"

void gc_collect(void) {
    // Conservatively scan the Wasm shadow stack.  Wasm locals that are never
    // address-taken live in the engine's value stack rather than in linear
    // memory, and so are invisible here -- which is why the build runs
    // `wasm-opt --spill-pointers`.  See README.md.
    void *dummy;
    gc_collect_start();
    gc_collect_root(&dummy, ((mp_uint_t)mp_wasm_stack_top - (mp_uint_t)&dummy) / sizeof(mp_uint_t));
    gc_collect_end();
}

// The only place output goes.  There is no fd to fall back to when nothing is
// capturing, and no terminal to cook line endings for, so mpconfigport.h
// points MP_PLAT_PRINT_STRN straight here.
mp_uint_t mp_hal_stdout_tx_strn(const char *str, size_t len) {
    if (mp_wasm_capture != NULL) {
        mp_api_buf_add(mp_wasm_capture, str, len);
    }
    return len;
}

// Unreachable in normal operation: every entry point in wasm_api.c runs under
// an nlr scope.  Trapping surfaces as a panic on the host, and costs nothing,
// where reporting the message would pull in stdio and proc_exit.
void nlr_jump_fail(void *val) {
    (void)val;
    __builtin_trap();
}

void MP_NORETURN __fatal_error(const char *msg) {
    (void)msg;
    __builtin_trap();
}

#ifndef NDEBUG
void MP_WEAK __assert_fail(const char *expr, const char *file, unsigned int line, const char *func) {
    (void)expr;
    (void)file;
    (void)line;
    (void)func;
    __fatal_error("assertion failed");
}
#endif

// Although we have no filesystem here, we unfortunately need to include 
// MICROPY_PY_IO as json.loads uses StringIO so opening anything fails the 
// way opening a missing file does.
mp_obj_t mp_builtin_open(size_t n_args, const mp_obj_t *args, mp_map_t *kwargs) {
    (void)n_args;
    (void)args;
    (void)kwargs;
    mp_raise_OSError(MP_ENOENT);
}
MP_DEFINE_CONST_FUN_OBJ_KW(mp_builtin_open_obj, 1, mp_builtin_open);
