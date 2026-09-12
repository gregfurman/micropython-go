#include <string.h>
#include <stdlib.h>

#include "py/objlist.h"
#include "py/objstr.h"
#include "py/builtin.h"
#include "py/parsenum.h"
#include "py/runtime.h"

#include "types.h"

#include "py/runtime.h"
#include "py/gc.h"

// Keeps host-held objects alive across GC. Picked up by the qstr/root-pointer
// extractor because micropython_embed.mk lists this file in SRC_QSTR.
MP_REGISTER_ROOT_POINTER(mp_obj_t host_refs);

static size_t ref_next;

void refs_init(void) {
    MP_STATE_VM(host_refs) = mp_obj_new_list(0, NULL);
    mp_obj_list_append(MP_STATE_VM(host_refs), MP_OBJ_NULL);  // slot 0 reserved
    ref_next = 1;
}

uint32_t ref_add(mp_obj_t obj) {
    mp_obj_list_t *l = MP_OBJ_TO_PTR(MP_STATE_VM(host_refs));
    while (ref_next < l->len) {
        if (l->items[ref_next] == MP_OBJ_NULL) {
            l->items[ref_next] = obj;
            return (uint32_t)ref_next++;
        }
        ++ref_next;
    }
    uint32_t id = (uint32_t)l->len;
    mp_obj_list_append(MP_STATE_VM(host_refs), obj);
    ref_next = id + 1;
    return id;
}

mp_obj_t ref_get(uint32_t id) {
    mp_obj_list_t *l = MP_OBJ_TO_PTR(MP_STATE_VM(host_refs));
    return (id > 0 && id < l->len) ? l->items[id] : MP_OBJ_NULL;
}

void refs_free(uint32_t id) {
    mp_obj_list_t *l = MP_OBJ_TO_PTR(MP_STATE_VM(host_refs));
    if (id > 0 && id < l->len) {
        l->items[id] = MP_OBJ_NULL;
        if (id < ref_next) {
            ref_next = id;
        }
    }
}

static inline void mp_value_set_f64(mp_value_t *v, double d) {
    uint64_t bits;
    memcpy(&bits, &d, sizeof(bits));
    v->w1 = (uint32_t)bits;
    v->w2 = (uint32_t)(bits >> 32);
}

static inline double mp_value_get_f64(const mp_value_t *v) {
    uint64_t bits = (uint64_t)v->w1 | ((uint64_t)v->w2 << 32);
    double d;
    memcpy(&d, &bits, sizeof(d));
    return d;
}

static inline void mp_value_set_i64(mp_value_t *v, int64_t n) {
    uint64_t bits;
    memcpy(&bits, &n, sizeof(bits));
    v->w1 = (uint32_t)bits;
    v->w2 = (uint32_t)(bits >> 32);
}

static inline int64_t mp_value_get_i64(const mp_value_t *v) {
    uint64_t bits = (uint64_t)v->w1 | ((uint64_t)v->w2 << 32);
    int64_t n;
    memcpy(&n, &bits, sizeof(n));
    return n;
}

// vstr storage belongs to MicroPython's GC heap. Values returned to the host
// must instead use the module allocator so the host can release them via free.
static void value_take_vstr(mp_value_t *out, uint32_t kind, vstr_t *text) {
    char *copy = NULL;
    if (text->len != 0) {
        copy = malloc(text->len);
        if (copy == NULL) {
            vstr_clear(text);
            mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("value transfer"));
        }
        memcpy(copy, text->buf, text->len);
    }
    out->kind = kind;
    out->w1 = text->len;
    out->w2 = (uintptr_t)copy;
    vstr_clear(text);
}

static mp_value_t* serialize_sequence(mp_obj_t seq, uint32_t *out_len) {
    size_t len;
    mp_obj_t *items;
    mp_obj_get_array(seq, &len, &items);
    *out_len = (uint32_t)len;

    if (len == 0) return NULL;

    mp_value_t *buf = malloc(len * sizeof(mp_value_t));
    if (!buf) return NULL;

    for (size_t i = 0; i < len; i++) {
        value_from_obj(items[i], &buf[i]);
    }
    return buf;
}

static mp_value_t* serialize_dict(mp_obj_t dict_in, uint32_t *out_used) {
    mp_obj_dict_t *dict = MP_OBJ_TO_PTR(dict_in);
    uint32_t used = dict->map.used;
    *out_used = used;

    if (used == 0) return NULL;

    // Allocate 2 mp_value_t structs per key/value pair
    mp_value_t *buf = malloc(used * 2 * sizeof(mp_value_t));
    if (!buf) return NULL;

    size_t idx = 0;
    for (size_t i = 0; i < dict->map.alloc; i++) {
        if (mp_map_slot_is_filled(&dict->map, i)) {
            value_from_obj(dict->map.table[i].key, &buf[idx++]);
            value_from_obj(dict->map.table[i].value, &buf[idx++]);
        }
    }
    return buf;
}

static mp_value_t* serialize_set(mp_obj_t set, uint32_t *out_len) {
    size_t len = (size_t)MP_OBJ_SMALL_INT_VALUE(mp_obj_len(set));
    *out_len = (uint32_t)len;
    if (len == 0) return NULL;

    mp_value_t *buf = malloc(len * sizeof(mp_value_t));
    if (!buf) return NULL;

    mp_obj_iter_buf_t iter_buf;
    mp_obj_t iter = mp_getiter(set, &iter_buf);
    mp_obj_t item;
    size_t i = 0;
    while ((item = mp_iternext(iter)) != MP_OBJ_STOP_ITERATION) {
        value_from_obj(item, &buf[i++]);
    }
    return buf;
}

static void value_from_printed(mp_value_t *out, uint32_t kind, mp_obj_t obj, mp_print_kind_t print_kind) {
    const char *type = mp_obj_get_type_str(obj);
    vstr_t text;
    mp_print_t print;
    vstr_init_print(&text, 64, &print);
    vstr_add_str(&text, type);
    vstr_add_char(&text, '\x04');
    mp_obj_print_helper(&print, obj, print_kind);
    value_take_vstr(out, kind, &text);
}

static mp_obj_t exception_from_value(const mp_value_t *in) {
    const char *blob = (const char *)(uintptr_t)in->w2;
    const char *sep = memchr(blob, '\x04', in->w1);
    size_t type_len = sep == NULL ? 0 : (size_t)(sep - blob);
    const char *message = sep == NULL ? blob : sep + 1;
    size_t message_len = sep == NULL ? in->w1 : in->w1 - type_len - 1;

    const mp_obj_type_t *type = &mp_type_HostError;
    if (type_len > 0) {
        qstr name = qstr_from_strn(blob, type_len);
        mp_map_elem_t *elem = mp_map_lookup(
            (mp_map_t *)&mp_module_builtins_globals.map,
            MP_OBJ_NEW_QSTR(name), MP_MAP_LOOKUP);
        if (elem != NULL && mp_obj_is_type(elem->value, &mp_type_type)
            && mp_obj_is_subclass_fast(elem->value, MP_OBJ_FROM_PTR(&mp_type_BaseException))) {
            type = MP_OBJ_TO_PTR(elem->value);
        }
    }

    mp_obj_t msg = mp_obj_new_str(message, message_len);
    free((void *)(uintptr_t)in->w2);
    return mp_obj_new_exception_arg1(type, msg);
}

void value_from_obj(mp_obj_t obj, mp_value_t *out) {
    out->w1 = 0;
    out->w2 = 0;

    if (obj == MP_OBJ_NULL)   { out->kind = KIND_NULL; return; }
    if (obj == mp_const_none) { out->kind = KIND_NONE; return; }

    if (obj == mp_const_true || obj == mp_const_false) {
        out->kind = KIND_BOOL;
        out->w1 = (obj == mp_const_true);
        return;
    }

    if (mp_obj_is_small_int(obj)) {
        int64_t v = (int64_t)MP_OBJ_SMALL_INT_VALUE(obj);
        out->kind = KIND_INT;
        mp_value_set_i64(out, v);
        return;
    }

    if (mp_obj_is_int(obj)) {
        long long v = mp_obj_get_ll(obj);
        if (mp_obj_equal(mp_obj_new_int_from_ll(v), obj)) {
            out->kind = KIND_INT;
            mp_value_set_i64(out, (int64_t)v);
        } else {
            value_from_printed(out, KIND_OBJECT, obj, PRINT_REPR);
        }
        return;
    }

    #if MICROPY_PY_BUILTINS_FLOAT
        if (mp_obj_is_float(obj)) {
            out->kind = KIND_FLOAT;
            mp_value_set_f64(out, (double)mp_obj_get_float(obj));
            return;
        }
    #endif

    if (mp_obj_is_str(obj) || mp_obj_is_type(obj, &mp_type_bytes)) {
        size_t len;
        const char *s = mp_obj_str_get_data(obj, &len);
        out->kind = mp_obj_is_str(obj) ? KIND_STR : KIND_BYTES;
        out->w1 = (uint32_t)len;
        out->w2 = (uintptr_t)s;
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_tuple) || mp_obj_is_type(obj, &mp_type_list)) {
        uint32_t len = 0;
        mp_value_t *buf = serialize_sequence(obj, &len);
        out->kind = mp_obj_is_type(obj, &mp_type_tuple) ? KIND_TUPLE : KIND_LIST;
        out->w1 = len;
        out->w2 = (uintptr_t)buf;
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_dict)) {
        uint32_t used = 0;
        mp_value_t *buf = serialize_dict(obj, &used);
        out->kind = KIND_DICT;
        out->w1 = used;
        out->w2 = (uintptr_t)buf;
        return;
    }

    #if MICROPY_PY_BUILTINS_SET
    if (mp_obj_is_type(obj, &mp_type_set)
        #if MICROPY_PY_BUILTINS_FROZENSET
        || mp_obj_is_type(obj, &mp_type_frozenset)
        #endif
    ) {
        uint32_t len = 0;
        mp_value_t *buf = serialize_set(obj, &len);
        #if MICROPY_PY_BUILTINS_FROZENSET
        out->kind = mp_obj_is_type(obj, &mp_type_frozenset) ? KIND_FROZENSET : KIND_SET;
        #else
        out->kind = KIND_SET;
        #endif
        out->w1 = len;
        out->w2 = (uintptr_t)buf;
        return;
    }
    #endif

    value_from_printed(out, KIND_OBJECT, obj, PRINT_REPR);
}


void value_from_exception(mp_obj_t exc, mp_value_t *out) {
    static const char fallback[] = "<unprintable exception>";
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        vstr_t vstr;
        mp_print_t pr;
        vstr_init_print(&vstr, 128, &pr);
        vstr_add_str(&vstr, mp_obj_get_type_str(exc));
        vstr_add_char(&vstr, '\x04');
        mp_obj_print_helper(&pr, exc, PRINT_STR);
        vstr_add_char(&vstr, '\x04');
        mp_obj_print_exception(&pr, exc);
        nlr_pop();
        value_take_vstr(out, KIND_EXCEPTION, &vstr);
    } else {
        out->kind = KIND_EXCEPTION;
        out->w1 = sizeof(fallback) - 1;
        char *copy = malloc(sizeof(fallback) - 1);
        if (copy == NULL) {
            out->w1 = 0;
            out->w2 = 0;
        } else {
            memcpy(copy, fallback, sizeof(fallback) - 1);
            out->w2 = (uintptr_t)copy;
        }
    }
}

mp_obj_t obj_from_value(const mp_value_t *in) {
    switch ((int32_t)in->kind) {
        case KIND_NULL:
            return MP_OBJ_NULL;
        case KIND_NONE:
            return mp_const_none;
        case KIND_BOOL:
            return mp_obj_new_bool(in->w1);
        case KIND_INT: {
            return mp_obj_new_int_from_ll(mp_value_get_i64(in));
        }
        case KIND_BIGINT: {
            mp_obj_t value = mp_parse_num_integer((const char *)(uintptr_t)in->w2, in->w1, 10, NULL);
            free((void *)(uintptr_t)in->w2);
            return value;
        }
        #if MICROPY_PY_BUILTINS_FLOAT
        case KIND_FLOAT:
            return mp_obj_new_float_from_d(mp_value_get_f64(in));
        #endif
        case KIND_STR: {
            mp_obj_t value = mp_obj_new_str((const char *)(uintptr_t)in->w2, in->w1);
            free((void *)(uintptr_t)in->w2);
            return value;
        }
        case KIND_BYTES: {
            mp_obj_t value = mp_obj_new_bytes((const byte *)(uintptr_t)in->w2, in->w1);
            free((void *)(uintptr_t)in->w2);
            return value;
        }
        case KIND_EXCEPTION:
            return exception_from_value(in);
        case KIND_REF:
        case KIND_CALLABLE:
        case KIND_OBJECT: {
            mp_obj_t o = ref_get(in->w1);
            if (o == MP_OBJ_NULL) {
                mp_raise_ValueError(MP_ERROR_TEXT("stale ref"));
            }
            return o;
        }
        case KIND_LIST:
        case KIND_TUPLE:
        case KIND_SET:
        case KIND_FROZENSET: {
            uint32_t len = in->w1;
            mp_value_t *buf = (mp_value_t *)(uintptr_t)in->w2;
            
            mp_obj_t seq = mp_obj_new_list(0, NULL);
            if (len > 0 && buf != NULL) {
                for (uint32_t i = 0; i < len; i++) {
                    mp_obj_list_append(seq, obj_from_value(&buf[i]));
                }
                free(buf);
            }
            
            if (in->kind == KIND_TUPLE) {
                seq = mp_obj_new_tuple(len, ((mp_obj_list_t*)MP_OBJ_TO_PTR(seq))->items);
            } else if (in->kind == KIND_SET || in->kind == KIND_FROZENSET) {
                seq = mp_obj_new_set(len, ((mp_obj_list_t*)MP_OBJ_TO_PTR(seq))->items);
                #if MICROPY_PY_BUILTINS_FROZENSET
                if (in->kind == KIND_FROZENSET) {
                    seq = mp_call_function_1(MP_OBJ_FROM_PTR(&mp_type_frozenset), seq);
                }
                #endif
            }
            return seq;
        }
        case KIND_DICT: {
            uint32_t num_pairs = in->w1;
            mp_value_t *buf = (mp_value_t *)(uintptr_t)in->w2;
            mp_obj_t dict = mp_obj_new_dict(num_pairs);
            
            if (num_pairs > 0 && buf != NULL) {
                for (uint32_t i = 0; i < num_pairs; i++) {
                    mp_obj_t key = obj_from_value(&buf[i * 2]);
                    mp_obj_t val = obj_from_value(&buf[(i * 2) + 1]);
                    mp_obj_dict_store(dict, key, val);
                }
                free(buf);
            }
            return dict;
        }
        default:
            mp_raise_ValueError(MP_ERROR_TEXT("bad host value"));
    }
}
