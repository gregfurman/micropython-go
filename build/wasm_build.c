/*
 * Turning host data into Python values.
 *
 * The obvious way to pass a value in is to render it as a literal and let the
 * module parse it.  That is the wrong shape: it makes the host responsible for
 * quoting, escaping and float round-tripping, it turns every argument into a
 * parse, and a bug in the rendering becomes code injection.
 *
 * Instead the host sends a flat prefix walk of the value, one byte of tag each,
 * and this file decodes it.  That is still serialisation, but a fixed binary
 * format has no quoting, no parse ambiguity, and no path by which a string in
 * the data becomes code.  It also arrives in a single crossing: an earlier
 * version pushed one value per call, and each of those paid for an nlr_push
 * whose setjmp forces every call inside it through an invoke_* trampoline,
 * which cost more than the decoding does.
 *
 * Decoding assembles values on a stack that is a Python list registered as a
 * GC root.  That is the part that has to be right: an mp_obj_t is a pointer
 * into the GC heap, so a half-built container held anywhere the collector
 * cannot see would be freed underneath us by the next allocation.
 */

#include <stdint.h>
#include <string.h>

#include "py/objlist.h"
#include "py/objstr.h"
#include "py/runtime.h"

#include "wasm_api.h"
#include "wasm_pack.h"

// The decode stack, and the callables the host has resolved to handles. Both
// are plain Python lists, so the GC traces them for free and they grow on
// demand.
MP_REGISTER_ROOT_POINTER(mp_obj_t mp_api_stack);
MP_REGISTER_ROOT_POINTER(mp_obj_t mp_api_funcs);

void mp_api_stack_init(void) {
    MP_STATE_VM(mp_api_stack) = mp_obj_new_list(0, NULL);
    MP_STATE_VM(mp_api_funcs) = mp_obj_new_list(0, NULL);
}

static mp_obj_list_t *stack(void) {
    return MP_OBJ_TO_PTR(MP_STATE_VM(mp_api_stack));
}

static mp_obj_list_t *funcs(void) {
    return MP_OBJ_TO_PTR(MP_STATE_VM(mp_api_funcs));
}

static void push(mp_obj_t value) {
    mp_obj_list_append(MP_STATE_VM(mp_api_stack), value);
}

// Returns the base index of the top n entries, checking there are that many.
static size_t take(size_t n) {
    if (n > stack()->len) {
        mp_raise_ValueError(MP_ERROR_TEXT("stack underflow"));
    }
    return stack()->len - n;
}

// --- decoding --------------------------------------------------------------

typedef struct _unpacker_t {
    const uint8_t *p;
    const uint8_t *end;
} unpacker_t;

static void need(unpacker_t *u, size_t n) {
    if ((size_t)(u->end - u->p) < n) {
        mp_raise_ValueError(MP_ERROR_TEXT("truncated argument buffer"));
    }
}

// The buffer is not aligned, so every multi-byte read goes through memcpy.
static uint32_t read_u32(unpacker_t *u) {
    need(u, 4);
    uint32_t v;
    memcpy(&v, u->p, sizeof(v));
    u->p += 4;
    return v;
}

static mp_obj_t new_int(int64_t value) {
    if (value >= INT32_MIN && value <= INT32_MAX) {
        // Promotes to a long int when too big to be a small int, or raises
        // OverflowError if this build has no long int support.
        return mp_obj_new_int((mp_int_t)value);
    }
    // Only reachable with arbitrary-precision integers enabled; raises
    // OverflowError otherwise rather than silently truncating.
    return mp_obj_new_int_from_ll(value);
}

// Pops the last n values (2n for a dict) and pushes the container holding
// them. The pop happens only after the container is allocated, so the items
// stay rooted across any collection that allocation triggers.
static void collect(uint8_t tag, size_t n) {
    size_t base;
    mp_obj_t collected;

    if (tag == PK_DICT) {
        base = take(2 * n);
        collected = mp_obj_new_dict(n);
        for (size_t i = 0; i < n; i++) {
            mp_obj_dict_store(collected, stack()->items[base + 2 * i],
                stack()->items[base + 2 * i + 1]);
        }
    } else if (tag == PK_SET || tag == PK_FROZENSET) {
        base = take(n);
        // Raises TypeError if an item is unhashable, which is the same answer
        // set() would give in Python.
        collected = mp_obj_new_set(n, &stack()->items[base]);
        #if MICROPY_PY_BUILTINS_FROZENSET
        if (tag == PK_FROZENSET) {
            collected = mp_call_function_1(MP_OBJ_FROM_PTR(&mp_type_frozenset), collected);
        }
        #endif
    } else {
        base = take(n);
        collected = (tag == PK_LIST)
            ? mp_obj_new_list(n, &stack()->items[base])
            : mp_obj_new_tuple(n, &stack()->items[base]);
    }

    stack()->len = base;
    push(collected);
}

// Decodes one value and leaves it on the stack.
static void unpack_one(unpacker_t *u, int depth) {
    mp_cstack_check();
    if (depth > PK_MAX_DEPTH) {
        mp_raise_ValueError(MP_ERROR_TEXT("argument nested too deeply"));
    }

    need(u, 1);
    uint8_t tag = *u->p++;

    switch (tag) {
        case PK_NONE:
            push(mp_const_none);
            return;

        case PK_FALSE:
            push(mp_const_false);
            return;

        case PK_TRUE:
            push(mp_const_true);
            return;

        case PK_INT: {
            need(u, 8);
            int64_t v;
            memcpy(&v, u->p, sizeof(v));
            u->p += 8;
            push(new_int(v));
            return;
        }

        case PK_FLOAT: {
            need(u, 8);
            double v;
            memcpy(&v, u->p, sizeof(v));
            u->p += 8;
            #if MICROPY_PY_BUILTINS_FLOAT
            push(mp_obj_new_float((mp_float_t)v));
            #else
            mp_raise_TypeError(MP_ERROR_TEXT("no float support"));
            #endif
            return;
        }

        case PK_STR:
        case PK_BYTES: {
            uint32_t len = read_u32(u);
            need(u, len);
            const char *data = (const char *)u->p;
            u->p += len;
            push(tag == PK_STR
                ? mp_obj_new_str(data, len)
                : mp_obj_new_bytes((const byte *)data, len));
            return;
        }

        case PK_LIST:
        case PK_TUPLE:
        case PK_SET:
        case PK_FROZENSET:
        case PK_DICT: {
            uint32_t n = read_u32(u);
            uint32_t values = (tag == PK_DICT) ? 2 * n : n;
            for (uint32_t i = 0; i < values; i++) {
                unpack_one(u, depth + 1);
            }
            collect(tag, n);
            return;
        }

        default:
            mp_raise_ValueError(MP_ERROR_TEXT("bad argument tag"));
    }
}

// Decodes n values onto a stack emptied first, so a call that failed part-way
// through cannot leave anything behind for the next one.
static void unpack_all(const uint8_t *ptr, uint32_t len, uint32_t n) {
    stack()->len = 0;
    unpacker_t u = { ptr, ptr + len };
    for (uint32_t i = 0; i < n; i++) {
        unpack_one(&u, 0);
    }
}

// --- exports ---------------------------------------------------------------

// Resolves a global once and returns a handle for it, or -1 with the error
// recorded. Worth doing when the same function is called repeatedly: it lifts
// the qstr interning and the global lookup out of the call.
int32_t mp_api_func(const char *name, uint32_t name_len) {
    MP_API_STACK_TOP();

    nlr_buf_t nlr;
    if (nlr_push(&nlr) != 0) {
        mp_api_store_error((mp_obj_t)nlr.ret_val);
        return -1;
    }

    mp_obj_t fun = mp_load_global(qstr_from_strn(name, name_len));
    if (!mp_obj_is_callable(fun)) {
        mp_raise_TypeError(MP_ERROR_TEXT("not callable"));
    }
    mp_obj_list_append(MP_STATE_VM(mp_api_funcs), fun);

    nlr_pop();
    return funcs()->len - 1;
}

// The whole invocation in one crossing: decode the arguments, call, and stream
// the result to the host's val_* callbacks.
int32_t mp_api_call(int32_t handle, const uint8_t *ptr, uint32_t len, uint32_t n_args) {
    MP_API_ENTER();

    if (handle < 0 || (size_t)handle >= funcs()->len) {
        mp_raise_ValueError(MP_ERROR_TEXT("bad function handle"));
    }

    unpack_all(ptr, len, n_args);
    size_t base = take(n_args);
    mp_obj_t result = mp_call_function_n_kw(funcs()->items[handle], n_args, 0,
        &stack()->items[base]);
    stack()->len = 0;
    mp_api_emit_value(result);

    MP_API_LEAVE();
}
