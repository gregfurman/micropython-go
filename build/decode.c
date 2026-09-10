// Host values back into guest objects. obj_from_value switches on the wire
// kind, allocating out of the guest heap: what the host sent is copied, and
// nothing the guest builds here points back into the transfer region.
//
// The exception is a handle. KIND_OBJECT and KIND_REF name a slot in the ref
// table, so they resolve to the very object that crossed rather than a copy of
// it, and a ref whose object has since been released fails here.

#include <string.h>

#include "py/builtin.h"
#include "py/cstack.h"
#include "py/objlist.h"
#include "py/objtuple.h"
#include "py/parsenum.h"
#include "py/runtime.h"
#include "refs.h"
#include "value.h"

// An exception blob is type, message and traceback separated by \x04. A type
// naming a builtin is rebuilt as that class; anything else becomes HostError,
// so guest code can tell a failed callback from an interpreter error.
static mp_obj_t exception_from_value(const mp_value_t* in) {
    const char* blob = (const char*)(uintptr_t)in->w2;
    const char* sep = memchr(blob, '\x04', in->w1);
    size_t type_len = sep == NULL ? 0 : (size_t)(sep - blob);
    const char* message = sep == NULL ? blob : sep + 1;
    size_t message_len = sep == NULL ? in->w1 : in->w1 - type_len - 1;

    const mp_obj_type_t* type = &mp_type_HostError;
    if (type_len > 0) {
        qstr name = qstr_from_strn(blob, type_len);
        mp_map_elem_t* elem =
            mp_map_lookup((mp_map_t*)&mp_module_builtins_globals.map, MP_OBJ_NEW_QSTR(name), MP_MAP_LOOKUP);
        if (elem != NULL && mp_obj_is_type(elem->value, &mp_type_type) &&
            mp_obj_is_subclass_fast(elem->value, MP_OBJ_FROM_PTR(&mp_type_BaseException))) {
            type = MP_OBJ_TO_PTR(elem->value);
        }
    }

    mp_obj_t msg = mp_obj_new_str(message, message_len);
    return mp_obj_new_exception_arg1(type, msg);
}

mp_obj_t obj_from_value(const mp_value_t* in) {
    mp_cstack_check();

    switch ((int32_t)in->kind) {
        case KIND_NULL:
            return MP_OBJ_NULL;
        case KIND_NONE:
            return mp_const_none;
        case KIND_BOOL:
            return mp_obj_new_bool(in->w1 != 0);
        case KIND_INT:
            return mp_obj_new_int_from_ll(mp_value_get_i64(in));
        case KIND_BIGINT:
            return mp_parse_num_integer((const char*)(uintptr_t)in->w2, in->w1, 10, NULL);
        case KIND_FLOAT:
            return mp_obj_new_float_from_d(mp_value_get_f64(in));
        case KIND_STR:
            return mp_obj_new_str((const char*)(uintptr_t)in->w2, in->w1);
        case KIND_BYTES:
            return mp_obj_new_bytes((const byte*)(uintptr_t)in->w2, in->w1);
        case KIND_EXCEPTION:
            return exception_from_value(in);
        case KIND_REF:
        case KIND_OBJECT: {
            mp_obj_t obj = ref_get(in->w1);

            if (obj == MP_OBJ_NULL) mp_raise_ValueError(MP_ERROR_TEXT("stale ref"));

            return obj;
        }

        case KIND_LIST:
        case KIND_TUPLE:
        case KIND_SET:
        case KIND_FROZENSET: {
            uint32_t len = in->w1;
            const mp_value_t* values = (const mp_value_t*)(uintptr_t)in->w2;

            if (len != 0 && values == NULL) mp_raise_ValueError(MP_ERROR_TEXT("null sequence buffer"));

            /*
             * Build a temporary list first.
             *
             * This keeps recursive decoding common between
             * list / tuple / set / frozenset.
             */
            mp_obj_t seq = mp_obj_new_list(0, NULL);

            for (uint32_t i = 0; i < len; i++) {
                mp_obj_list_append(seq, obj_from_value(&values[i]));
            }

            if (in->kind == KIND_TUPLE) {
                mp_obj_list_t* list = MP_OBJ_TO_PTR(seq);
                return mp_obj_new_tuple(len, list->items);
            }

            if (in->kind == KIND_SET || in->kind == KIND_FROZENSET) {
                mp_obj_list_t* list = MP_OBJ_TO_PTR(seq);
                mp_obj_t set = mp_obj_new_set(len, list->items);

                if (in->kind == KIND_FROZENSET) return mp_call_function_1(MP_OBJ_FROM_PTR(&mp_type_frozenset), set);

                return set;
            }

            return seq;
        }

        case KIND_DICT: {
            uint32_t len = in->w1;

            const mp_value_t* values = (const mp_value_t*)(uintptr_t)in->w2;

            if (len != 0 && values == NULL) mp_raise_ValueError(MP_ERROR_TEXT("null dict buffer"));

            mp_obj_t dict = mp_obj_new_dict(len);

            for (uint32_t i = 0; i < len; i++) {
                mp_obj_t key = obj_from_value(&values[i * 2]);

                mp_obj_t value = obj_from_value(&values[i * 2 + 1]);

                mp_obj_dict_store(dict, key, value);
            }

            return dict;
        }

        default:
            mp_raise_ValueError(MP_ERROR_TEXT("bad host value"));
    }
}
