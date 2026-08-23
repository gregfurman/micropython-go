/*
 * The embedding API: everything the host can call on this module.
 *
 * The ABI is deliberately narrow -- every export takes and returns scalars,
 * and anything larger is passed as (pointer, length) into linear memory. That
 * is the same discipline ncruces/go-sqlite3-wasm applies with its `_go` shims:
 * wasm2go can only carry i32/i64/f32/f64 across the boundary, so function
 * pointers, structs and varargs get bound here in C rather than being
 * expressed from Go.
 *
 * The module is a reactor (-mexec-model=reactor): wasi-libc's _initialize runs
 * the constructors, one of which boots the interpreter, and from then on the
 * host drives everything from here.  There is no main().
 */

#include <stdlib.h>
#include <string.h>

#include "py/compile.h"
#include "py/cstack.h"
#include "py/gc.h"
#include "py/runtime.h"

#include "wasm_api.h"
#include "wasm_proxy.h"

static char heap[MICROPY_HEAP_SIZE];

// Highest shadow stack address seen by the current host call; gc_collect()
// scans down from here.  Re-recorded on entry to every export, because each
// host call starts a fresh C stack.
char *mp_wasm_stack_top;

// What the current call printed, and the traceback if it raised.  Both are
// reset per call and stay valid until the next one.
static mp_api_buf_t out_buf;
static mp_api_buf_t err_buf;

// While set, mp_hal_stdout_tx_strn() appends here.
mp_api_buf_t *mp_wasm_capture = NULL;

// --- buffers ---------------------------------------------------------------

// Grows buf to hold at least size bytes, plus room for a NUL.  Returns false
// if the allocation failed, leaving the buffer as it was.
static bool buf_reserve(mp_api_buf_t *buf, size_t size) {
    if (size + 1 <= buf->cap) {
        return true;
    }
    size_t cap = buf->cap ? buf->cap : 256;
    while (cap < size + 1) {
        cap *= 2;
    }
    char *data = realloc(buf->data, cap);
    if (data == NULL) {
        return false;
    }
    buf->data = data;
    buf->cap = cap;
    return true;
}

void mp_api_buf_add(mp_api_buf_t *buf, const char *str, size_t len) {
    if (!buf_reserve(buf, buf->len + len)) {
        return; // drop output rather than fail the call
    }
    memcpy(buf->data + buf->len, str, len);
    buf->len += len;
    buf->data[buf->len] = '\0';
}

void mp_api_buf_free(mp_api_buf_t *buf) {
    free(buf->data);
    *buf = (mp_api_buf_t) { 0 };
}

void mp_api_buf_print_strn(void *data, const char *str, size_t len) {
    mp_api_buf_add((mp_api_buf_t *)data, str, len);
}

static void buf_reset(mp_api_buf_t *buf) {
    buf->len = 0;
    if (buf->data != NULL) {
        buf->data[0] = '\0';
    }
}

static const char *buf_cstr(const mp_api_buf_t *buf) {
    return buf->data != NULL ? buf->data : "";
}

// The exception the last failed call raised, kept alive so the host can ask
// for its structure after the fact.  Always overwritten before the host is
// told a call failed, so it is never stale when read.
MP_REGISTER_ROOT_POINTER(mp_obj_t mp_api_last_exc);

// Called from the failure branch of an nlr_push, where an escaping exception
// would abort the module.  Printing runs Python -- __str__ on the exception,
// __repr__ on its args -- so it gets its own guard, and a failure part-way
// leaves the host whatever was written before it.
void mp_api_store_error(mp_obj_t exc) {
    buf_reset(&err_buf);
    MP_STATE_VM(mp_api_last_exc) = exc;

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        mp_print_t print = { &err_buf, mp_api_buf_print_strn };
        mp_obj_print_exception(&print, exc);
        nlr_pop();
    }
}

// Streams the last exception to the host as a native value; see the shape in
// wasm_value.c.  Separate from mp_api_store_error so the host decides when the
// walk happens: it runs Python code, and doing that while unwinding another
// exception is how a module aborts.
int32_t mp_api_err_value(void) {
    MP_API_STACK_TOP();

    mp_obj_t exc = MP_STATE_VM(mp_api_last_exc);
    if (exc == MP_OBJ_NULL) {
        return MP_API_ERR;
    }

    nlr_buf_t nlr;
    if (nlr_push(&nlr) != 0) {
        return MP_API_ERR;
    }
    mp_api_emit_error(exc);
    MP_API_LEAVE();
}

// Asked by the VM hook every MICROPY_VM_HOOK_COUNT instructions; non-zero means
// the host wants this call to stop.
__attribute__((import_module("host"), import_name("poll")))
extern int32_t host_poll(void);

// Raising here unwinds through the interpreter's own nlr machinery, so the
// guest sees an ordinary exception and the host sees an ordinary error.
void mp_api_vm_poll(void) {
    if (host_poll()) {
        mp_raise_type(&mp_type_KeyboardInterrupt);
    }
}

// --- entry points ----------------------------------------------------------

// Runs as part of _initialize, so the host has a working interpreter as soon
// as the module is instantiated.
__attribute__((constructor)) static void mp_api_boot(void) {
    MP_API_STACK_TOP();

    gc_init(heap, heap + sizeof(heap));
    mp_init();
    mp_api_stack_init();
    mp_api_proxy_init();
}

int32_t mp_api_eval(const char *src, uint32_t src_len, uint32_t mode) {
    MP_API_STACK_TOP();

    buf_reset(&out_buf);
    buf_reset(&err_buf);

    mp_parse_input_kind_t kind =
        (mode == MP_API_EXEC) ? MP_PARSE_FILE_INPUT : MP_PARSE_EVAL_INPUT;

    int32_t ret = MP_API_OK;
    nlr_buf_t nlr;
    mp_wasm_capture = &out_buf;
    if (nlr_push(&nlr) == 0) {
        mp_lexer_t *lex = mp_lexer_new_from_str_len(MP_QSTR__lt_string_gt_, src, src_len, 0);
        qstr source_name = lex->source_name;
        mp_parse_tree_t parse_tree = mp_parse(lex, kind);
        mp_obj_t module_fun = mp_compile(&parse_tree, source_name, false);
        mp_obj_t value = mp_call_function_0(module_fun);
        if (mode == MP_API_VALUE) {
            mp_api_emit_value(value);
        }
        nlr_pop();
    } else {
        // Format the traceback into err_buf rather than letting it escape, so
        // the host can turn it into an error value.
        mp_api_store_error((mp_obj_t)nlr.ret_val);
        ret = MP_API_ERR;
    }
    mp_wasm_capture = NULL;

    return ret;
}

// Pointers are linear memory offsets, valid until the next call.

const char *mp_api_out(void) {
    return buf_cstr(&out_buf);
}

uint32_t mp_api_out_len(void) {
    return out_buf.len;
}

const char *mp_api_err(void) {
    return buf_cstr(&err_buf);
}

uint32_t mp_api_err_len(void) {
    return err_buf.len;
}

// Somewhere for the host to put the bytes it is about to hand over: source
// text, a packed argument list, a name.  Every consumer copies out of it
// before returning, so one buffer is enough.  It can move when it grows, so
// the host must ask for it again each time.
static mp_api_buf_t scratch;

void *mp_api_scratch(uint32_t size) {
    return buf_reserve(&scratch, size) ? scratch.data : NULL;
}
