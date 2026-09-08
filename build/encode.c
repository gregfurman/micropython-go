// Guest objects out to the host. value_from_mp_obj picks a writer by Python type
// and every writer puts its payload in the same arena, so one result is one
// contiguous region the host reads without following anything back into the
// guest heap.
//
// Two kinds of value do not get copied. An object with no wire representation
// crosses as a handle, and so does a container that cannot be walked: one
// reaching itself, or one nested past MP_VALUE_MAX_DEPTH. Neither acquires a
// reference here. Both are recorded in pending, so a tree that fails partway
// costs nothing to unwind; the committed entry points below acquire ids for the
// survivors and patch them into the records the walk left behind.
//
// pending_refs_t never leaves this file. Callers see only the committed entry
// points, which own the whole acquire-or-unwind cycle.

#include <string.h>

#include "py/cstack.h"
#include "py/objlist.h"
#include "py/objtuple.h"
#include "py/runtime.h"
#include "refs.h"
#include "value.h"

// objset.c keeps the set object private, so this mirrors its layout.
typedef struct {
    mp_obj_base_t base;
    mp_set_t set;
} host_set_t;

// One opaque value waiting for an id: the object, and the record whose w1 will
// name it once commit has run.
typedef struct {
    mp_obj_t obj;
    mp_value_t* value;
} pending_ref_t;

typedef struct {
    pending_ref_t* items;
    size_t len;
    size_t capacity;
} pending_refs_t;

// -----------------------------------------------------------------------------
// Private helpers
// -----------------------------------------------------------------------------

static void value_take_vstr(mp_arena_t* arena, mp_value_t* out, uint32_t kind, vstr_t* text);

static void pending_refs_add(pending_refs_t* pending, mp_obj_t obj, mp_value_t* value);
static void pending_refs_clear(pending_refs_t* pending);
static void pending_refs_commit(mp_arena_t* arena, pending_refs_t* pending);

static bool obj_is_iterator(mp_obj_t obj);
static void value_from_mp_opaque(
    mp_arena_t* arena, pending_refs_t* pending, mp_value_t* out, mp_obj_t obj, uint32_t attributes);
static bool value_from_mp_handle(mp_arena_t* arena, pending_refs_t* pending, mp_value_t* out, mp_obj_t obj);
static void value_from_mp_bigint(mp_arena_t* arena, mp_value_t* out, mp_obj_t obj);
static void value_from_mp_byte_string(mp_arena_t* arena, mp_value_t* out, mp_obj_t obj);
static void value_from_mp_sequence(mp_arena_t* arena, pending_refs_t* pending, mp_value_t* out, mp_obj_t obj);
static void value_from_mp_set(mp_arena_t* arena, pending_refs_t* pending, mp_value_t* out, mp_obj_t obj);
static void value_from_mp_dict(mp_arena_t* arena, pending_refs_t* pending, mp_value_t* out, mp_obj_t obj);
static void value_from_mp_obj(mp_arena_t* arena, pending_refs_t* pending, mp_obj_t obj, mp_value_t* out);

// -----------------------------------------------------------------------------
// Public encoding entry points
// -----------------------------------------------------------------------------

void value_from_mp_objs_committed(mp_arena_t* arena, const mp_obj_t* objs, size_t n, mp_value_t* out) {
    pending_refs_t pending = {0};
    nlr_buf_t nlr;

    if (nlr_push(&nlr) == 0) {
        for (size_t i = 0; i < n; i++) value_from_mp_obj(arena, &pending, objs[i], &out[i]);

        pending_refs_commit(arena, &pending);

        nlr_pop();

        pending_refs_clear(&pending);
        return;
    }

    pending_refs_clear(&pending);
    nlr_jump(nlr.ret_val);
}

void value_from_mp_obj_committed(mp_arena_t* arena, mp_obj_t obj, mp_value_t* out) {
    value_from_mp_objs_committed(arena, &obj, 1, out);
}

// An exception packs type, message and traceback into one blob separated by
// \x04. It needs no ref, so it survives the arena reset that discards the
// partly written result it replaces.
void value_from_mp_exception(mp_arena_t* arena, mp_obj_t exc, mp_value_t* out) {
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
        value_take_vstr(arena, out, KIND_EXCEPTION, &text);
        // Copying can overflow too. Keep the handler until every potentially
        // raising step has finished; the fallback below never raises.
        nlr_pop();
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

// -----------------------------------------------------------------------------
// Pending refs
// -----------------------------------------------------------------------------

static void pending_refs_add(pending_refs_t* pending, mp_obj_t obj, mp_value_t* value) {
    if (pending->len == pending->capacity) {
        size_t old_capacity = pending->capacity;
        size_t new_capacity = old_capacity == 0 ? 8 : old_capacity * 2;
        pending->items = m_renew(pending_ref_t, pending->items, old_capacity, new_capacity);
        pending->capacity = new_capacity;
    }

    pending->items[pending->len++] = (pending_ref_t){.obj = obj, .value = value};
}

static void pending_refs_clear(pending_refs_t* pending) {
    if (pending->items != NULL) {
        m_del(pending_ref_t, pending->items, pending->capacity);
    }
    pending->items = NULL;
    pending->len = 0;
    pending->capacity = 0;
}

static void pending_refs_commit(mp_arena_t* arena, pending_refs_t* pending) {
    if (pending->len == 0) {
        return;
    }
    if (pending->len > UINT32_MAX / sizeof(uint32_t)) {
        mp_raise_ValueError(MP_ERROR_TEXT("too many references"));
    }
    // Reserve the whole ledger before acquiring anything. No allocation may
    // separate ref_add from recording the acquisition in this array.
    uint32_t* refs = arena_alloc(arena, pending->len * sizeof(uint32_t));
    if (refs == NULL) {
        mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("arena full"));
    }
    volatile size_t committed = 0;
    nlr_buf_t nlr;

    if (nlr_push(&nlr) == 0) {
        for (size_t i = 0; i < pending->len; i++) {
            uint32_t ref = ref_add(pending->items[i].obj);
            refs[i] = ref;
            pending->items[i].value->w1 = ref;
            committed++;
        }
        nlr_pop();
        mp_transfer_t* transfer = (mp_transfer_t*)arena->base;
        transfer->refs = (uint32_t)(uintptr_t)refs;
        transfer->num_refs = pending->len;
        return;
    }

    while (committed != 0) {
        committed--;
        ref_release(refs[committed]);
    }

    nlr_jump(nlr.ret_val);
}

// -----------------------------------------------------------------------------
// Writers
// -----------------------------------------------------------------------------

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

// An object with no wire form crosses as a handle. Nothing is written for it
// beyond its attributes: w1 is filled in once the caller has acquired an id.
static void value_from_mp_opaque(
    mp_arena_t* arena, pending_refs_t* pending, mp_value_t* out, mp_obj_t obj, uint32_t attributes) {
    // NOTE(gregfurman):
    const char* name = mp_obj_get_type_str(obj);
    size_t len = strlen(name);

    if (len > UINT32_MAX - sizeof(mp_object_class_t)) mp_raise_ValueError(MP_ERROR_TEXT("value too large"));

    mp_object_class_t* class = arena_alloc(arena, sizeof(mp_object_class_t) + (uint32_t)len);
    if (class == NULL) mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("arena full"));

    class->len = (uint32_t)len;
    memcpy(class->name, name, len);

    out->kind = KIND_OBJECT;
    out->w1 = 0;  // patched with ref id during commit
    out->w2 = (uint32_t)(uintptr_t)class | attributes;

    pending_refs_add(pending, obj, out);
}

// value_from_mp_handle reports whether obj has to cross as a handle rather than a
// copy, writing it out if so.
static bool value_from_mp_handle(mp_arena_t* arena, pending_refs_t* pending, mp_value_t* out, mp_obj_t obj) {
    if (arena->depth >= MP_VALUE_MAX_DEPTH) {
        value_from_mp_opaque(arena, pending, out, obj, 0);
        return true;
    }

    for (const mp_active_t* node = arena->active; node != NULL; node = node->prev) {
        if (node->obj == obj) {
            value_from_mp_opaque(arena, pending, out, obj, 0);
            return true;
        }
    }
    return false;
}

static void value_from_mp_bigint(mp_arena_t* arena, mp_value_t* out, mp_obj_t obj) {
    vstr_t text;
    mp_print_t print;

    vstr_init_print(&text, 32, &print);
    mp_obj_print_helper(&print, obj, PRINT_STR);
    value_take_vstr(arena, out, KIND_BIGINT, &text);
}

static void value_from_mp_byte_string(mp_arena_t* arena, mp_value_t* out, mp_obj_t obj) {
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

static void value_from_mp_sequence(mp_arena_t* arena, pending_refs_t* pending, mp_value_t* out, mp_obj_t obj) {
    size_t len;
    mp_obj_t* items;

    mp_obj_get_array(obj, &len, &items);

    if (len > UINT32_MAX / sizeof(mp_value_t)) mp_raise_ValueError(MP_ERROR_TEXT("sequence too large"));

    mp_value_t* values = arena_alloc(arena, (uint32_t)len * sizeof(mp_value_t));

    if (len != 0 && values == NULL) mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("arena full"));

    mp_obj_tuple_t* snapshot;
    if (mp_obj_is_type(obj, &mp_type_tuple)) {
        out->kind = KIND_TUPLE;
        snapshot = MP_OBJ_TO_PTR(obj);
    } else if (mp_obj_is_type(obj, &mp_type_list)) {
        out->kind = KIND_LIST;
        // A child's __repr__ can resize or clear this list. The temporary
        // tuple also keeps removed children rooted if __repr__ runs the GC.
        snapshot = MP_OBJ_TO_PTR(mp_obj_new_tuple(len, items));
    } else {
        mp_raise_TypeError(MP_ERROR_TEXT("expected tuple or list"));
    }

    out->w1 = (uint32_t)len;
    out->w2 = (uint32_t)(uintptr_t)values;

    mp_active_t frame;
    arena_push(arena, &frame, obj);
    for (size_t i = 0; i < len; i++) value_from_mp_obj(arena, pending, snapshot->items[i], &values[i]);
    arena_pop(arena, &frame);
}

static void value_from_mp_set(mp_arena_t* arena, pending_refs_t* pending, mp_value_t* out, mp_obj_t obj) {
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

    // Copy every entry before serialization can call back into Python.
    mp_obj_tuple_t* snapshot = MP_OBJ_TO_PTR(mp_obj_new_tuple(len, NULL));
    size_t i = 0;
    for (size_t slot = 0; slot < set->set.alloc; slot++) {
        if (!mp_set_slot_is_filled(&set->set, slot)) continue;
        snapshot->items[i++] = set->set.table[slot];
    }

    mp_active_t frame;
    arena_push(arena, &frame, obj);
    for (size_t i = 0; i < len; i++) value_from_mp_obj(arena, pending, snapshot->items[i], &values[i]);
    arena_pop(arena, &frame);
}

static void value_from_mp_dict(mp_arena_t* arena, pending_refs_t* pending, mp_value_t* out, mp_obj_t obj) {
    mp_obj_dict_t* dict = MP_OBJ_TO_PTR(obj);
    size_t len = dict->map.used;

    if (len > UINT32_MAX / (2 * sizeof(mp_value_t))) mp_raise_ValueError(MP_ERROR_TEXT("dict too large"));

    mp_value_t* values = arena_alloc(arena, (uint32_t)len * 2 * sizeof(mp_value_t));

    if (len != 0 && values == NULL) mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("arena full"));

    out->kind = KIND_DICT;
    out->w1 = (uint32_t)len;
    out->w2 = (uint32_t)(uintptr_t)values;

    mp_obj_tuple_t* snapshot = MP_OBJ_TO_PTR(mp_obj_new_tuple(2 * len, NULL));
    size_t i = 0;
    for (size_t slot = 0; slot < dict->map.alloc; slot++) {
        if (!mp_map_slot_is_filled(&dict->map, slot)) continue;

        mp_map_elem_t* elem = &dict->map.table[slot];
        snapshot->items[2 * i] = elem->key;
        snapshot->items[2 * i + 1] = elem->value;
        i++;
    }

    mp_active_t frame;
    arena_push(arena, &frame, obj);
    for (size_t i = 0; i < 2 * len; i++) value_from_mp_obj(arena, pending, snapshot->items[i], &values[i]);
    arena_pop(arena, &frame);
}

static void value_from_mp_obj(mp_arena_t* arena, pending_refs_t* pending, mp_obj_t obj, mp_value_t* out) {
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
            value_from_mp_bigint(arena, out, obj);
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
        value_from_mp_byte_string(arena, out, obj);
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_tuple) || mp_obj_is_type(obj, &mp_type_list)) {
        if (value_from_mp_handle(arena, pending, out, obj)) {
            return;
        }

        value_from_mp_sequence(arena, pending, out, obj);
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_dict)) {
        if (value_from_mp_handle(arena, pending, out, obj)) {
            return;
        }

        value_from_mp_dict(arena, pending, out, obj);
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_set) || mp_obj_is_type(obj, &mp_type_frozenset)) {
        if (value_from_mp_handle(arena, pending, out, obj)) {
            return;
        }

        value_from_mp_set(arena, pending, out, obj);
        return;
    }

    uint32_t attributes = 0;

    if (obj_is_iterator(obj)) {
        attributes |= KIND_OBJECT_ATTR_ITERABLE;
    }

    if (mp_obj_is_callable(obj)) {
        attributes |= KIND_OBJECT_ATTR_CALLABLE;
    }

    value_from_mp_opaque(arena, pending, out, obj, attributes);
}
