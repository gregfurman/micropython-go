#include "types.h"

#include <stdlib.h>
#include <string.h>

#include "py/builtin.h"
#include "py/cstack.h"
#include "py/objlist.h"
#include "py/parsenum.h"
#include "py/runtime.h"

// objset.c keeps the set object private, so this mirrors its layout.
typedef struct {
    mp_obj_base_t base;
    mp_set_t set;
} host_set_t;

MP_REGISTER_ROOT_POINTER(mp_obj_t host_refs);
MP_REGISTER_ROOT_POINTER(mp_obj_t host_ref_meta);
MP_REGISTER_ROOT_POINTER(mp_obj_t host_ref_ids);

// A ref is a slot index plus the generation the slot had when it was handed
// out, so an id outliving its object is rejected instead of naming whatever
// took the slot next. One object gets one slot however many times it crosses,
// counted so the slot is freed only once the host has dropped them all. A
// count that saturates pins the slot rather than risk freeing it early.
#define REF_INDEX_BITS 20
#define REF_INDEX_MASK ((1u << REF_INDEX_BITS) - 1)
#define REF_GEN_MASK 0xfffu
#define REF_COUNT_PINNED 0xffffu

static size_t ref_next;

static mp_map_t* ref_ids(void) { return mp_obj_dict_get_map(MP_STATE_VM(host_ref_ids)); }

// Guest pointers fit a small int, so this allocates nothing.
static mp_obj_t ref_key(mp_obj_t obj) { return mp_obj_new_int_from_uint((uintptr_t)obj); }

static uint32_t ref_meta(size_t index) {
    mp_obj_list_t* m = MP_OBJ_TO_PTR(MP_STATE_VM(host_ref_meta));
    return (uint32_t)MP_OBJ_SMALL_INT_VALUE(m->items[index]);
}

static uint32_t ref_gen(size_t index) { return ref_meta(index) >> 16; }

static uint32_t ref_count(size_t index) { return ref_meta(index) & 0xffffu; }

static void ref_meta_set(size_t index, uint32_t gen, uint32_t count) {
    mp_obj_list_t* m = MP_OBJ_TO_PTR(MP_STATE_VM(host_ref_meta));
    m->items[index] = MP_OBJ_NEW_SMALL_INT((gen << 16) | count);
}

static bool obj_is_iterator(mp_obj_t obj) {
    const mp_obj_type_t* type = mp_obj_get_type(obj);
    if ((type->flags & (MP_TYPE_FLAG_ITER_IS_ITERNEXT | MP_TYPE_FLAG_ITER_IS_CUSTOM | MP_TYPE_FLAG_ITER_IS_STREAM)) !=
        0) {
        return true;
    }

    mp_obj_t dest[2];
    mp_load_method_maybe(obj, MP_QSTR___next__, dest);
    return dest[0] != MP_OBJ_NULL;
}

// Reset the roots which keep values handed to the host alive.  This is used
// after restoring a memory snapshot: its host refs belong to the old timeline,
// and the Go handles for them have been invalidated by the host epoch.
void refs_reset(void) {
    MP_STATE_VM(host_refs) = mp_obj_new_list(0, NULL);
    MP_STATE_VM(host_ref_meta) = mp_obj_new_list(0, NULL);
    MP_STATE_VM(host_ref_ids) = mp_obj_new_dict(0);
    mp_obj_list_append(MP_STATE_VM(host_refs), MP_OBJ_NULL);  // slot 0 reserved
    mp_obj_list_append(MP_STATE_VM(host_ref_meta), MP_OBJ_NEW_SMALL_INT(1 << 16));
    ref_next = 1;
}

uint32_t ref_add(mp_obj_t obj) {
    mp_map_elem_t* elem = mp_map_lookup(ref_ids(), ref_key(obj), MP_MAP_LOOKUP_ADD_IF_NOT_FOUND);
    if (elem->value != MP_OBJ_NULL) {
        size_t index = (size_t)MP_OBJ_SMALL_INT_VALUE(elem->value);
        uint32_t count = ref_count(index);
        if (count < REF_COUNT_PINNED) {
            ref_meta_set(index, ref_gen(index), count + 1);
        }
        return (uint32_t)index | (ref_gen(index) << REF_INDEX_BITS);
    }

    size_t index;
    mp_obj_list_t* l = MP_OBJ_TO_PTR(MP_STATE_VM(host_refs));
    while (ref_next < l->len && l->items[ref_next] != MP_OBJ_NULL) {
        ++ref_next;
    }
    if (ref_next < l->len) {
        index = ref_next++;
        l->items[index] = obj;
        ref_meta_set(index, ref_gen(index), 1);
    } else {
        index = l->len;
        if (index > REF_INDEX_MASK) {
            mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("too many host refs"));
        }
        // Meta first: a failed append here leaves the lists usable.
        mp_obj_list_append(MP_STATE_VM(host_ref_meta), MP_OBJ_NEW_SMALL_INT((1 << 16) | 1));
        mp_obj_list_append(MP_STATE_VM(host_refs), obj);
        ref_next = index + 1;
    }

    elem->value = MP_OBJ_NEW_SMALL_INT(index);
    return (uint32_t)index | (ref_gen(index) << REF_INDEX_BITS);
}

mp_obj_t ref_get(uint32_t id) {
    size_t index = id & REF_INDEX_MASK;
    mp_obj_list_t* l = MP_OBJ_TO_PTR(MP_STATE_VM(host_refs));
    if (index == 0 || index >= l->len || l->items[index] == MP_OBJ_NULL || (id >> REF_INDEX_BITS) != ref_gen(index)) {
        return MP_OBJ_NULL;
    }
    return l->items[index];
}

void refs_free(uint32_t id) {
    size_t index = id & REF_INDEX_MASK;
    mp_obj_list_t* l = MP_OBJ_TO_PTR(MP_STATE_VM(host_refs));
    if (index == 0 || index >= l->len || l->items[index] == MP_OBJ_NULL) {
        return;
    }

    uint32_t gen = ref_gen(index);
    if ((id >> REF_INDEX_BITS) != gen) {
        return;
    }

    uint32_t count = ref_count(index);
    if (count == REF_COUNT_PINNED) {
        return;
    }
    if (count > 1) {
        ref_meta_set(index, gen, count - 1);
        return;
    }

    mp_map_lookup(ref_ids(), ref_key(l->items[index]), MP_MAP_LOOKUP_REMOVE_IF_FOUND);

    uint32_t next = (gen + 1) & REF_GEN_MASK;
    ref_meta_set(index, next == 0 ? 1 : next, 0);
    l->items[index] = MP_OBJ_NULL;
    if (index < ref_next) {
        ref_next = index;
    }
}

static inline void mp_value_set_f64(mp_value_t* v, double d) {
    uint64_t bits;
    memcpy(&bits, &d, sizeof(bits));
    v->w1 = (uint32_t)bits;
    v->w2 = (uint32_t)(bits >> 32);
}

static inline double mp_value_get_f64(const mp_value_t* v) {
    uint64_t bits = (uint64_t)v->w1 | ((uint64_t)v->w2 << 32);
    double d;
    memcpy(&d, &bits, sizeof(d));
    return d;
}

static inline void mp_value_set_i64(mp_value_t* v, int64_t n) {
    uint64_t bits;
    memcpy(&bits, &n, sizeof(bits));
    v->w1 = (uint32_t)bits;
    v->w2 = (uint32_t)(bits >> 32);
}

static inline int64_t mp_value_get_i64(const mp_value_t* v) {
    uint64_t bits = (uint64_t)v->w1 | ((uint64_t)v->w2 << 32);
    int64_t n;
    memcpy(&n, &bits, sizeof(n));
    return n;
}

static void* arena_alloc(mp_arena_t* arena, uint32_t size);

static void value_take_vstr(mp_arena_t* arena, mp_value_t* out, uint32_t kind, vstr_t* text) {
    char* copy = NULL;
    if (text->len != 0) {
        if (text->len > UINT32_MAX) {
            vstr_clear(text);
            mp_raise_ValueError(MP_ERROR_TEXT("value too large"));
        }

        copy = arena_alloc(arena, (uint32_t)text->len);
        if (copy == NULL) {
            vstr_clear(text);
            mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("arena full"));
        }
        memcpy(copy, text->buf, text->len);
    }

    out->kind = kind;
    out->w1 = (uint32_t)text->len;
    out->w2 = (uint32_t)(uintptr_t)copy;

    vstr_clear(text);
}
typedef struct {
    uint32_t len;
    char blob[];
} object_info_t;

// Object values keep both a guest ref and a printable description.  The ref
// occupies w1; w2 is an aligned object_info_t pointer with its low bits used
// for attributes, leaving the description length in the sidecar header.
static void value_take_object(mp_value_t* out, mp_obj_t obj, uint32_t attributes, vstr_t* text) {
    object_info_t* info = malloc(sizeof(*info) + text->len);
    if (info == NULL) {
        vstr_clear(text);
        mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("object transfer"));
    }

    info->len = text->len;
    memcpy(info->blob, text->buf, text->len);
    out->kind = KIND_OBJECT;
    out->w1 = ref_add(obj);
    out->w2 = (uint32_t)(uintptr_t)info | attributes;
    vstr_clear(text);
}

// An object crosses as a handle carrying its type and repr. Containers do not
// come through here, since printing one recurses over what the host is about to
// read for itself.
static void value_from_object(mp_value_t* out, mp_obj_t obj, uint32_t attributes) {
    vstr_t text;
    mp_print_t print;
    vstr_init_print(&text, 64, &print);
    vstr_add_str(&text, mp_obj_get_type_str(obj));
    vstr_add_char(&text, '\x04');
    mp_obj_print_helper(&print, obj, PRINT_REPR);
    value_take_object(out, obj, attributes, &text);
}

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

static void* arena_alloc(mp_arena_t* a, uint32_t size) {
    uint32_t offset = (a->offset + 3u) & ~3u;

    if (offset > a->capacity || size > a->capacity - offset) {
        a->overflowed = true;
        return NULL;
    }

    void* ptr = a->base + offset;
    a->offset = offset + size;

    return ptr;
}

// arena_push marks obj as being copied, so anything nested inside it can tell
// that reaching obj again closes a cycle. The frame lives in the caller's own
// stack frame, so tracking costs no allocation.
static void arena_push(mp_arena_t* a, mp_active_t* frame, mp_obj_t obj) {
    frame->obj = obj;
    frame->prev = a->active;
    a->active = frame;
    a->depth++;
}

static void arena_pop(mp_arena_t* a, mp_active_t* frame) {
    a->active = frame->prev;
    a->depth--;
}

// Some containers cannot be copied here: one reachable from itself has no
// finite copy, and one past the depth bound would cost more of the region than
// it is worth. Both cross the way any other object does, as a handle the host
// resolves separately when it wants the rest.
//
// The description is written rather than printed, since printing a container
// recurses over exactly what we have declined to walk.
static void value_from_container_handle(mp_value_t* out, mp_obj_t obj) {
    const char* ellipsis = "[...]";
    if (mp_obj_is_type(obj, &mp_type_dict)) {
        ellipsis = "{...}";
    } else if (mp_obj_is_type(obj, &mp_type_tuple)) {
        ellipsis = "(...)";
    }

    vstr_t text;
    vstr_init(&text, 24);
    vstr_add_str(&text, mp_obj_get_type_str(obj));
    vstr_add_char(&text, '\x04');
    vstr_add_str(&text, ellipsis);
    value_take_object(out, obj, 0, &text);
}

// value_from_handle reports whether obj has to cross as a handle rather than a
// copy, writing it out if so.
static bool value_from_handle(mp_arena_t* arena, mp_value_t* out, mp_obj_t obj) {
    if (arena->depth >= MP_VALUE_MAX_DEPTH) {
        value_from_container_handle(out, obj);
        return true;
    }

    for (const mp_active_t* node = arena->active; node != NULL; node = node->prev) {
        if (node->obj == obj) {
            value_from_container_handle(out, obj);
            return true;
        }
    }
    return false;
}

static void value_from_byte_string(mp_arena_t* arena, mp_value_t* out, mp_obj_t obj) {
    size_t len;
    const char* src = mp_obj_str_get_data(obj, &len);

    if (len > UINT32_MAX) mp_raise_ValueError(MP_ERROR_TEXT("value too large"));

    void* dst = NULL;
    if (len != 0) {
        dst = arena_alloc(arena, (uint32_t)len);
        if (dst == NULL) mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("arena full"));

        memcpy(dst, src, len);
    }

    out->kind = mp_obj_is_str(obj) ? KIND_STR : KIND_BYTES;
    out->w1 = (uint32_t)len;
    out->w2 = (uint32_t)(uintptr_t)dst;
}

static void value_from_sequence(mp_arena_t* arena, mp_value_t* out, mp_obj_t obj) {
    size_t len;
    mp_obj_t* items;

    mp_obj_get_array(obj, &len, &items);

    if (len > UINT32_MAX / sizeof(mp_value_t)) mp_raise_ValueError(MP_ERROR_TEXT("sequence too large"));

    mp_value_t* values = arena_alloc(arena, (uint32_t)len * sizeof(mp_value_t));

    if (len != 0 && values == NULL) mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("arena full"));

    if mp_obj_is_type (obj, &mp_type_tuple)
        out->kind = KIND_TUPLE;
    else if (mp_obj_is_type(obj, &mp_type_list))
        out->kind = KIND_LIST;
    else
        mp_raise_TypeError(MP_ERROR_TEXT("expected tuple or list"));

    out->w1 = (uint32_t)len;
    out->w2 = (uint32_t)(uintptr_t)values;

    mp_active_t frame;
    arena_push(arena, &frame, obj);
    for (size_t i = 0; i < len; i++) value_from_obj(arena, items[i], &values[i]);
    arena_pop(arena, &frame);
}

static void value_from_set(mp_arena_t* arena, mp_value_t* out, mp_obj_t obj) {
    uint32_t kind = KIND_INVALID;
    if (mp_obj_is_type(obj, &mp_type_frozenset))
        kind = KIND_FROZENSET;
    else if (mp_obj_is_type(obj, &mp_type_set))
        kind = KIND_SET;
    else
        mp_raise_TypeError(MP_ERROR_TEXT("expected set or frozenset"));

    host_set_t* set = MP_OBJ_TO_PTR(obj);
    size_t len = set->set.used;

    if (len > UINT32_MAX / sizeof(mp_value_t)) mp_raise_ValueError(MP_ERROR_TEXT("set too large"));

    mp_value_t* values = arena_alloc(arena, (uint32_t)len * sizeof(mp_value_t));

    if (len != 0 && values == NULL) mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("arena full"));

    out->kind = kind;
    out->w1 = (uint32_t)len;
    out->w2 = (uint32_t)(uintptr_t)values;

    size_t i = 0;

    mp_active_t frame;
    arena_push(arena, &frame, obj);
    for (size_t slot = 0; slot < set->set.alloc; slot++) {
        if (!mp_set_slot_is_filled(&set->set, slot)) continue;

        value_from_obj(arena, set->set.table[slot], &values[i]);

        i++;
    }
    arena_pop(arena, &frame);
}

static void value_from_dict(mp_arena_t* arena, mp_value_t* out, mp_obj_t obj) {
    mp_obj_dict_t* dict = MP_OBJ_TO_PTR(obj);
    size_t len = dict->map.used;

    if (len > UINT32_MAX / (2 * sizeof(mp_value_t))) mp_raise_ValueError(MP_ERROR_TEXT("dict too large"));

    mp_value_t* values = arena_alloc(arena, (uint32_t)len * 2 * sizeof(mp_value_t));

    if (len != 0 && values == NULL) mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("arena full"));

    out->kind = KIND_DICT;
    out->w1 = (uint32_t)len;
    out->w2 = (uint32_t)(uintptr_t)values;

    size_t i = 0;

    mp_active_t frame;
    arena_push(arena, &frame, obj);
    for (size_t slot = 0; slot < dict->map.alloc; slot++) {
        if (!mp_map_slot_is_filled(&dict->map, slot)) continue;

        mp_map_elem_t* elem = &dict->map.table[slot];

        value_from_obj(arena, elem->key, &values[i * 2]);

        value_from_obj(arena, elem->value, &values[i * 2 + 1]);

        i++;
    }
    arena_pop(arena, &frame);
}

void value_from_obj(mp_arena_t* arena, mp_obj_t obj, mp_value_t* out) {
    mp_cstack_check();

    out->w1 = 0;
    out->w2 = 0;

    if (obj == MP_OBJ_NULL) {
        out->kind = KIND_NULL;
        return;
    }
    if (obj == mp_const_none) {
        out->kind = KIND_NONE;
        return;
    }

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
            value_from_object(out, obj, 0);
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
        value_from_byte_string(arena, out, obj);
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_tuple) || mp_obj_is_type(obj, &mp_type_list)) {
        if (value_from_handle(arena, out, obj)) return;
        value_from_sequence(arena, out, obj);
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_dict)) {
        if (value_from_handle(arena, out, obj)) return;
        value_from_dict(arena, out, obj);
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_set) || mp_obj_is_type(obj, &mp_type_frozenset)) {
        if (value_from_handle(arena, out, obj)) return;
        value_from_set(arena, out, obj);
        return;
    }

    uint32_t obj_attributes = 0;

    if (obj_is_iterator(obj)) obj_attributes |= KIND_OBJECT_ATTR_ITERABLE;

    if (mp_obj_is_callable(obj)) obj_attributes |= KIND_OBJECT_ATTR_CALLABLE;

    // NOTE: This ensures the object is added to our global refs table
    value_from_object(out, obj, obj_attributes);
}

void value_from_exception(mp_arena_t* arena, mp_obj_t exc, mp_value_t* out) {
    static const char fallback[] = "<unprintable exception>";

    out->kind = KIND_EXCEPTION;
    out->w1 = 0;
    out->w2 = 0;

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        vstr_t text;
        mp_print_t print;
        vstr_init_print(&text, 128, &print);
        vstr_add_str(&text, mp_obj_get_type_str(exc));
        vstr_add_char(&text, '\x04');
        mp_obj_print_helper(&print, exc, PRINT_STR);
        vstr_add_char(&text, '\x04');
        mp_obj_print_exception(&print, exc);
        nlr_pop();
        value_take_vstr(arena, out, KIND_EXCEPTION, &text);
        return;
    }

    const uint32_t len = sizeof(fallback) - 1;

    char* copy = arena_alloc(arena, len);
    if (copy == NULL) {
        out->kind = KIND_EXCEPTION;
        out->w1 = 0;
        out->w2 = 0;
        return;
    }

    memcpy(copy, fallback, len);

    out->kind = KIND_EXCEPTION;
    out->w1 = len;
    out->w2 = (uint32_t)(uintptr_t)copy;
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
