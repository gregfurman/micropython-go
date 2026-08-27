#include <string.h>
#include <stdlib.h>

#include "py/objlist.h"
#include "py/objstr.h"
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
        int32_t v = (int32_t)MP_OBJ_SMALL_INT_VALUE(obj);
        out->kind = KIND_INT;
        memcpy(&out->w1, &v, sizeof(v));
        return;
    }

    if (mp_obj_is_int(obj)) {
        vstr_t vstr;
        mp_print_t pr;
        vstr_init_print(&vstr, 24, &pr);
        mp_obj_print_helper(&pr, obj, PRINT_STR);
        out->kind = KIND_BIGINT;
        out->w1 = vstr.len;
        out->w2 = (uintptr_t)vstr.buf;
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

    out->kind = mp_obj_is_callable(obj) ? KIND_CALLABLE : KIND_OBJECT;
    out->w1 = ref_add(obj);
}


void value_from_exception(mp_obj_t exc, mp_value_t *out) {
    static const char fallback[] = "<unprintable exception>";
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        vstr_t vstr;
        mp_print_t pr;
        vstr_init_print(&vstr, 128, &pr);
        vstr_add_str(&vstr, qstr_str(mp_obj_get_type(exc)->name));
        vstr_add_char(&vstr, '\x04');
        mp_obj_print_exception(&pr, exc);
        nlr_pop();
        out->kind = KIND_EXCEPTION;
        out->w1 = vstr.len;
        out->w2 = (uintptr_t)vstr.buf;
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
            int32_t v;
            memcpy(&v, &in->w1, sizeof(v));
            return mp_obj_new_int(v);
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
        case KIND_TUPLE: {
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
