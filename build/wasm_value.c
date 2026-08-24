/*
 * Handing Python values to the host.
 *
 * An mp_obj_t is a tagged pointer into the GC heap, and the host has no way to
 * keep one rooted across a collection, so the value is walked here and streamed
 * through the val_* callbacks instead.  Each carries one decoded scalar, so the
 * host builds its own values with no intermediate encoding.  A container
 * announces its length and is followed by exactly that many values, twice that
 * for a dict, which is enough to reassemble the tree with a small stack.
 */

#include <stdint.h>

#include "py/cstack.h"
#include "py/obj.h"
#include "py/objstr.h"
#include "py/runtime.h"

#include "wasm_api.h"
#include "wasm_pack.h"

#define HOST_IMPORT(name) __attribute__((import_module("host"), import_name(#name)))

// Scalars.
HOST_IMPORT(val_none) extern void host_val_none(void);
HOST_IMPORT(val_bool) extern void host_val_bool(int32_t value);
HOST_IMPORT(val_int) extern void host_val_int(int64_t value);
HOST_IMPORT(val_float) extern void host_val_float(double value);
HOST_IMPORT(val_str) extern void host_val_str(const char *ptr, uint32_t len);
HOST_IMPORT(val_bytes) extern void host_val_bytes(const char *ptr, uint32_t len);

// Containers: each is followed by its contents.
HOST_IMPORT(val_list) extern void host_val_list(uint32_t len);
HOST_IMPORT(val_tuple) extern void host_val_tuple(uint32_t len);
HOST_IMPORT(val_dict) extern void host_val_dict(uint32_t len);
HOST_IMPORT(val_set) extern void host_val_set(uint32_t len, int32_t frozen);

// Anything with no host equivalent, passed as its type name plus repr().
HOST_IMPORT(val_other) extern void host_val_other(const char *type, uint32_t type_len,
    const char *repr, uint32_t repr_len);

// An exception, as its class name and its message.
HOST_IMPORT(val_exception) extern void host_val_exception(const char *type, uint32_t type_len,
    const char *msg, uint32_t msg_len);

// Guards against a self-referential container turning the walk into an
// infinite recursion.  Python allows `a = []; a.append(a)`.
#define MAX_DEPTH (32)

static void emit_repr(mp_obj_t obj) {
    const char *type = mp_obj_get_type_str(obj);
    mp_api_buf_t repr = { 0 };
    mp_print_t print = { &repr, mp_api_buf_print_strn };
    mp_obj_print_helper(&print, obj, PRINT_REPR);
    host_val_other(type, strlen(type), repr.data ? repr.data : "", repr.len);
    mp_api_buf_free(&repr);
}

static void emit_value(mp_obj_t obj, int depth);

#if MICROPY_PY_BUILTINS_SET
static void emit_set(mp_obj_t obj, bool frozen, int depth) {
    host_val_set(MP_OBJ_SMALL_INT_VALUE(mp_obj_len(obj)), frozen);

    mp_obj_iter_buf_t iter_buf;
    mp_obj_t iter = mp_getiter(obj, &iter_buf);
    mp_obj_t item;
    while ((item = mp_iternext(iter)) != MP_OBJ_STOP_ITERATION) {
        emit_value(item, depth + 1);
    }
}
#endif

static void emit_seq(mp_obj_t obj, bool is_list, int depth) {
    size_t len;
    mp_obj_t *items;
    mp_obj_get_array(obj, &len, &items);

    if (is_list) {
        host_val_list(len);
    } else {
        host_val_tuple(len);
    }
    for (size_t i = 0; i < len; i++) {
        emit_value(items[i], depth + 1);
    }
}

static void emit_str_data(mp_obj_t obj, bool as_bytes) {
    size_t len;
    const char *str = mp_obj_str_get_data(obj, &len);
    if (as_bytes) {
        host_val_bytes(str, len);
    } else {
        host_val_str(str, len);
    }
}

static void emit_value(mp_obj_t obj, int depth) {
    mp_cstack_check();

    if (depth > MAX_DEPTH) {
        emit_repr(obj);
        return;
    }

    if (obj == mp_const_none) {
        host_val_none();
        return;
    }

    // Before the integer check: a bool satisfies mp_obj_get_int_maybe().
    if (mp_obj_is_bool(obj)) {
        host_val_bool(obj == mp_const_true);
        return;
    }

    if (mp_obj_is_int(obj)) {
        if (mp_obj_is_small_int(obj)) {
            host_val_int(MP_OBJ_SMALL_INT_VALUE(obj));
            return;
        }
        // Too big for the host's int64, so it goes over as a reference rather
        // than silently truncated.
        long long value = mp_obj_get_ll(obj);
        if (mp_obj_equal(mp_obj_new_int_from_ll(value), obj)) {
            host_val_int(value);
        } else {
            emit_repr(obj);
        }
        return;
    }

    #if MICROPY_PY_BUILTINS_FLOAT
    mp_float_t float_value;
    if (mp_obj_is_float(obj) && mp_obj_get_float_maybe(obj, &float_value)) {
        host_val_float(float_value);
        return;
    }
    #endif

    if (mp_obj_is_str(obj)) {
        emit_str_data(obj, false);
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_bytes)) {
        emit_str_data(obj, true);
        return;
    }

    #if MICROPY_PY_BUILTINS_BYTEARRAY
    // Through the buffer protocol: mp_obj_str_get_data is for str and bytes
    // only.  Reaches the host as []byte, same as bytes.
    if (mp_obj_is_type(obj, &mp_type_bytearray)) {
        mp_buffer_info_t buf;
        if (mp_get_buffer(obj, &buf, MP_BUFFER_READ)) {
            host_val_bytes(buf.buf, buf.len);
            return;
        }
    }
    #endif

    if (mp_obj_is_type(obj, &mp_type_dict)) {
        mp_map_t *map = mp_obj_dict_get_map(obj);
        host_val_dict(map->used);
        for (size_t i = 0; i < map->alloc; i++) {
            if (mp_map_slot_is_filled(map, i)) {
                emit_value(map->table[i].key, depth + 1);
                emit_value(map->table[i].value, depth + 1);
            }
        }
        return;
    }

    #if MICROPY_PY_BUILTINS_SET
    #if MICROPY_PY_BUILTINS_FROZENSET
    if (mp_obj_is_type(obj, &mp_type_frozenset)) {
        emit_set(obj, true, depth);
        return;
    }
    #endif
    if (mp_obj_is_type(obj, &mp_type_set)) {
        emit_set(obj, false, depth);
        return;
    }
    #endif

    if (mp_obj_is_type(obj, &mp_type_list)) {
        emit_seq(obj, true, depth);
        return;
    }
    if (mp_obj_is_type(obj, &mp_type_tuple)) {
        emit_seq(obj, false, depth);
        return;
    }

    emit_repr(obj);
}

void mp_api_emit_value(mp_obj_t obj) {
    emit_value(obj, 0);
}

// --- exceptions -------------------------------------------------------------

/*
 * An exception goes over as an exception: its class name and its message, in
 * one callback the host turns straight into the value it hands back.  It used
 * to be a 2-tuple the host reassembled by convention, which worked and left
 * nothing to say so -- this is the same information with a shape the other
 * side can name.
 *
 * The traceback is not taken apart: it is already in the text
 * mp_api_store_error printed, which is what a caller displays, and picking it
 * into fields would mean walking .args and the frame list -- both of which run
 * Python at a point where the module has just failed.
 *
 * Nothing here builds a Python object either; the callback is driven directly,
 * so a walk that runs while memory is exhausted still gets through.
 */

void mp_api_emit_error(mp_obj_t exc) {
    // Both of these work on whatever was raised, so there is no need to check
    // that it is an exception instance -- anything at all can reach here.
    const char *type = mp_obj_get_type_str(exc);

    // str(exc): the message alone, without the type name PRINT_EXC prefixes.
    mp_api_buf_t msg = { 0 };
    mp_print_t print = { &msg, mp_api_buf_print_strn };
    mp_obj_print_helper(&print, exc, PRINT_STR);

    host_val_exception(type, strlen(type), msg.data ? msg.data : "", msg.len);
    mp_api_buf_free(&msg);
}
