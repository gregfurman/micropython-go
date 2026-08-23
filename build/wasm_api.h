// The embedding API exported to the host.  See wasm_api.c.

#ifndef MICROPY_INCLUDED_WASI_WASM_API_H
#define MICROPY_INCLUDED_WASI_WASM_API_H

#include <stddef.h>
#include <stdint.h>

#include "py/cstack.h"
#include "py/nlr.h"
#include "py/obj.h"

// mp_api_eval() modes.
#define MP_API_EXEC  (0) // run as a script; the result is whatever it printed
#define MP_API_VALUE (1) // evaluate one expression and stream it to the host's
                         // val_* callbacks as a native host value

// mp_api_* return codes.
#define MP_API_OK  (0)
#define MP_API_ERR (1)

// A growable byte buffer backed by libc malloc, deliberately not by vstr:
// vstr allocates from the Python GC heap, and a buffer held only by a C static
// is not a GC root, so it could be collected while the host still needs it.
typedef struct _mp_api_buf_t {
    char *data;
    size_t len;
    size_t cap;
} mp_api_buf_t;

void mp_api_buf_add(mp_api_buf_t *buf, const char *str, size_t len);
void mp_api_buf_free(mp_api_buf_t *buf);
// Signature-compatible with mp_print_t's print_strn.
void mp_api_buf_print_strn(void *buf, const char *str, size_t len);

// Walks obj and streams it to the host's val_* callbacks; see wasm_value.c.
void mp_api_emit_value(mp_obj_t obj);

// Streams exc to the host's val_* callbacks as a (type, message) tuple; see
// wasm_value.c.  Printing the message runs Python, so it must be called under
// an nlr_push.
void mp_api_emit_error(mp_obj_t exc);

// Records the C stack base for this host call. Every export that can run
// Python needs it, because each host call starts a fresh C stack and the GC
// scans down from here.
#define MP_API_STACK_TOP()                                       \
    int _stack_dummy;                                            \
    mp_wasm_stack_top = (char *)&_stack_dummy;                   \
    mp_cstack_init_with_top(mp_wasm_stack_top, MICROPY_C_STACK_SIZE)

// Wraps an export body so a Python exception becomes MP_API_ERR plus a
// traceback in the error buffer, rather than an uncaught NLR that aborts.
#define MP_API_ENTER()                                           \
    MP_API_STACK_TOP();                                          \
    nlr_buf_t _nlr;                                              \
    if (nlr_push(&_nlr) != 0) {                                  \
        mp_api_store_error((mp_obj_t)_nlr.ret_val);              \
        return MP_API_ERR;                                       \
    }                                                            \
    do {} while (0)

#define MP_API_LEAVE()                                           \
    nlr_pop();                                                   \
    return MP_API_OK

// Formats exc into the error buffer that mp_api_err() exposes.
void mp_api_store_error(mp_obj_t exc);

// Creates the decode stack and the handle table; called from the constructor
// in wasm_api.c.
void mp_api_stack_init(void);

#define MP_API_EXPORT(name) __attribute__((export_name(#name)))

MP_API_EXPORT(mp_api_eval) int32_t mp_api_eval(const char *src, uint32_t src_len, uint32_t mode);

MP_API_EXPORT(mp_api_out) const char *mp_api_out(void);
MP_API_EXPORT(mp_api_out_len) uint32_t mp_api_out_len(void);
MP_API_EXPORT(mp_api_err) const char *mp_api_err(void);
MP_API_EXPORT(mp_api_err_len) uint32_t mp_api_err_len(void);
MP_API_EXPORT(mp_api_err_value) int32_t mp_api_err_value(void);

// Turning host data into Python values; see wasm_build.c.  Arguments arrive
// encoded in a single buffer.
MP_API_EXPORT(mp_api_func) int32_t mp_api_func(const char *name, uint32_t name_len);
MP_API_EXPORT(mp_api_call) int32_t mp_api_call(int32_t handle, const uint8_t *ptr, uint32_t len, uint32_t n_args);

// Decodes one value in the same format, without disturbing a decode already in
// progress; see wasm_build.c.
mp_obj_t mp_api_unpack(const uint8_t *ptr, uint32_t len);

// Builds the exception a PK_EXCEPTION names; see wasm_build.c.
mp_obj_t mp_api_new_exception(mp_obj_t name, mp_obj_t message);

// Decoding for an entry point nested inside another; see wasm_build.c and the
// note in wasm_proxy.c about why it cannot clear the stack.
mp_obj_t *mp_api_unpack_args(const uint8_t *ptr, uint32_t len, uint32_t n, size_t *mark);
void mp_api_unpack_rewind(size_t mark);

// Binding host functions and host values into the guest's globals; see
// wasm_host.c.  mp_api_reply is where the host writes what a host function
// returned.
MP_API_EXPORT(mp_api_register) int32_t mp_api_register(const char *name, uint32_t name_len, int32_t id);
MP_API_EXPORT(mp_api_set) int32_t mp_api_set(const char *name, uint32_t name_len, const uint8_t *ptr, uint32_t len);
MP_API_EXPORT(mp_api_reply) void *mp_api_reply(uint32_t size);

MP_API_EXPORT(mp_api_scratch) void *mp_api_scratch(uint32_t size);

// Set by the API entry points; used by gc_collect() in main.c.
extern char *mp_wasm_stack_top;

// When non-NULL, mp_hal_stdout_tx_strn() appends here instead of writing to
// fd 1.
extern mp_api_buf_t *mp_wasm_capture;

#endif // MICROPY_INCLUDED_WASI_WASM_API_H
