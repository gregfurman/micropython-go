/*
 * Handing Python values to the host as native host values.
 *
 * A MicroPython value is an mp_obj_t: a tagged pointer into the GC heap. The
 * host cannot hold one, because it has no way to keep it rooted across a
 * collection, so something has to cross the boundary instead of the pointer.
 *
 * Serialising to JSON would work, but it costs an encode on this side and a
 * parse on the other, and it flattens types the host cares about: ints and
 * floats both become JSON numbers, bytes have no representation, and tuples
 * and lists become indistinguishable.
 *
 * Instead the value is walked here and streamed to the host through imported
 * callbacks, the same way ncruces/go-sqlite3-wasm calls back into Go for its
 * VFS. Each callback carries one already-decoded scalar, so the host builds
 * its own native values directly, with no intermediate encoding. Containers
 * announce their length first and are then followed by exactly that many
 * values (twice that, alternating key and value, for a dict), which is enough
 * for the host to reassemble the tree with a small explicit stack.
 *
 * The type dispatch below leans on py/obj.c rather than reimplementing itself:
 * mp_obj_get_array() handles lists and tuples (and subclasses of them) in one
 * call, and the *_maybe() accessors do the int/float unwrapping.
 */

#include <stdint.h>

#include "py/cstack.h"
#include "py/obj.h"
#include "py/objstr.h"
#include "py/runtime.h"

#include "wasm_api.h"

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

// Anything with no host equivalent, passed as its type name plus repr().
HOST_IMPORT(val_other) extern void host_val_other(const char *type, uint32_t type_len,
    const char *repr, uint32_t repr_len);

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
        // Raises OverflowError past int64, which is the same limit the host
        // has; a small int takes the fast path inside.
        host_val_int(mp_obj_get_ll(obj));
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
        size_t len;
        const char *str = mp_obj_str_get_data(obj, &len);
        host_val_str(str, len);
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_bytes)) {
        size_t len;
        const char *str = mp_obj_str_get_data(obj, &len);
        host_val_bytes(str, len);
        return;
    }

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

    // mp_obj_get_array covers list and tuple in one call, so only the
    // list-vs-tuple tag has to be decided here.
    if (mp_obj_is_type(obj, &mp_type_list) || mp_obj_is_type(obj, &mp_type_tuple)) {
        size_t len;
        mp_obj_t *items;
        mp_obj_get_array(obj, &len, &items);
        if (mp_obj_is_type(obj, &mp_type_list)) {
            host_val_list(len);
        } else {
            host_val_tuple(len);
        }
        for (size_t i = 0; i < len; i++) {
            emit_value(items[i], depth + 1);
        }
        return;
    }

    emit_repr(obj);
}

void mp_api_emit_value(mp_obj_t obj) {
    emit_value(obj, 0);
}

// --- exceptions -------------------------------------------------------------

/*
 * An exception goes over as an ordinary value, in the shape
 *
 *     (type, message)
 *
 * which the host reassembles with the decoder it already has.  The traceback
 * is not taken apart: it is already in the text mp_api_store_error printed,
 * which is what a caller displays, and picking it into fields would mean
 * walking .args and the frame list -- both of which run Python at a point
 * where the module has just failed.
 *
 * Nothing here builds a Python object either; the callbacks are driven
 * directly, so a walk that runs while memory is exhausted still gets through.
 *
 * Keep this in step with lastError in internal/host/exports.go.
 */

void mp_api_emit_error(mp_obj_t exc) {
    host_val_tuple(2);

    // Both of these work on whatever was raised, so there is no need to check
    // that it is an exception instance -- anything at all can reach here.
    const char *type = mp_obj_get_type_str(exc);
    host_val_str(type, strlen(type));

    // str(exc): the message alone, without the type name PRINT_EXC prefixes.
    mp_api_buf_t msg = { 0 };
    mp_print_t print = { &msg, mp_api_buf_print_strn };
    mp_obj_print_helper(&print, exc, PRINT_STR);
    host_val_str(msg.data ? msg.data : "", msg.len);
    mp_api_buf_free(&msg);
}
