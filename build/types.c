#include <string.h>
#include <stdlib.h>

#include "py/objlist.h"
#include "py/builtin.h"
#include "py/parsenum.h"
#include "py/runtime.h"
#include "py/cstack.h"

#include "types.h"

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

static mp_map_t *ref_ids(void)
{
    return mp_obj_dict_get_map(MP_STATE_VM(host_ref_ids));
}

// Guest pointers fit a small int, so this allocates nothing.
static mp_obj_t ref_key(mp_obj_t obj)
{
    return mp_obj_new_int_from_uint((uintptr_t)obj);
}

static uint32_t ref_meta(size_t index)
{
    mp_obj_list_t *m = MP_OBJ_TO_PTR(MP_STATE_VM(host_ref_meta));
    return (uint32_t)MP_OBJ_SMALL_INT_VALUE(m->items[index]);
}

static uint32_t ref_gen(size_t index) { return ref_meta(index) >> 16; }

static uint32_t ref_count(size_t index) { return ref_meta(index) & 0xffffu; }

static void ref_meta_set(size_t index, uint32_t gen, uint32_t count)
{
    mp_obj_list_t *m = MP_OBJ_TO_PTR(MP_STATE_VM(host_ref_meta));
    m->items[index] = MP_OBJ_NEW_SMALL_INT((gen << 16) | count);
}

static bool obj_is_iterator(mp_obj_t obj)
{
    const mp_obj_type_t *type = mp_obj_get_type(obj);
    if ((type->flags & (MP_TYPE_FLAG_ITER_IS_ITERNEXT | MP_TYPE_FLAG_ITER_IS_CUSTOM | MP_TYPE_FLAG_ITER_IS_STREAM)) != 0)
    {
        return true;
    }

    mp_obj_t dest[2];
    mp_load_method_maybe(obj, MP_QSTR___next__, dest);
    return dest[0] != MP_OBJ_NULL;
}

// Reset the roots which keep values handed to the host alive.  This is used
// after restoring a memory snapshot: its host refs belong to the old timeline,
// and the Go handles for them have been invalidated by the host epoch.
void refs_reset(void)
{
    MP_STATE_VM(host_refs) = mp_obj_new_list(0, NULL);
    MP_STATE_VM(host_ref_meta) = mp_obj_new_list(0, NULL);
    MP_STATE_VM(host_ref_ids) = mp_obj_new_dict(0);
    mp_obj_list_append(MP_STATE_VM(host_refs), MP_OBJ_NULL); // slot 0 reserved
    mp_obj_list_append(MP_STATE_VM(host_ref_meta), MP_OBJ_NEW_SMALL_INT(1 << 16));
    ref_next = 1;
}

uint32_t ref_add(mp_obj_t obj)
{
    mp_map_elem_t *elem = mp_map_lookup(ref_ids(), ref_key(obj), MP_MAP_LOOKUP_ADD_IF_NOT_FOUND);
    if (elem->value != MP_OBJ_NULL)
    {
        size_t index = (size_t)MP_OBJ_SMALL_INT_VALUE(elem->value);
        uint32_t count = ref_count(index);
        if (count < REF_COUNT_PINNED)
        {
            ref_meta_set(index, ref_gen(index), count + 1);
        }
        return (uint32_t)index | (ref_gen(index) << REF_INDEX_BITS);
    }

    size_t index;
    mp_obj_list_t *l = MP_OBJ_TO_PTR(MP_STATE_VM(host_refs));
    while (ref_next < l->len && l->items[ref_next] != MP_OBJ_NULL)
    {
        ++ref_next;
    }
    if (ref_next < l->len)
    {
        index = ref_next++;
        l->items[index] = obj;
        ref_meta_set(index, ref_gen(index), 1);
    }
    else
    {
        index = l->len;
        if (index > REF_INDEX_MASK)
        {
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

mp_obj_t ref_get(uint32_t id)
{
    size_t index = id & REF_INDEX_MASK;
    mp_obj_list_t *l = MP_OBJ_TO_PTR(MP_STATE_VM(host_refs));
    if (index == 0 || index >= l->len || l->items[index] == MP_OBJ_NULL ||
        (id >> REF_INDEX_BITS) != ref_gen(index))
    {
        return MP_OBJ_NULL;
    }
    return l->items[index];
}

void refs_free(uint32_t id)
{
    size_t index = id & REF_INDEX_MASK;
    mp_obj_list_t *l = MP_OBJ_TO_PTR(MP_STATE_VM(host_refs));
    if (index == 0 || index >= l->len || l->items[index] == MP_OBJ_NULL)
    {
        return;
    }

    uint32_t gen = ref_gen(index);
    if ((id >> REF_INDEX_BITS) != gen)
    {
        return;
    }

    uint32_t count = ref_count(index);
    if (count == REF_COUNT_PINNED)
    {
        return;
    }
    if (count > 1)
    {
        ref_meta_set(index, gen, count - 1);
        return;
    }

    mp_map_lookup(ref_ids(), ref_key(l->items[index]), MP_MAP_LOOKUP_REMOVE_IF_FOUND);

    uint32_t next = (gen + 1) & REF_GEN_MASK;
    ref_meta_set(index, next == 0 ? 1 : next, 0);
    l->items[index] = MP_OBJ_NULL;
    if (index < ref_next)
    {
        ref_next = index;
    }
}

static inline void mp_value_set_f64(mp_value_t *v, double d)
{
    uint64_t bits;
    memcpy(&bits, &d, sizeof(bits));
    v->w1 = (uint32_t)bits;
    v->w2 = (uint32_t)(bits >> 32);
}

static inline double mp_value_get_f64(const mp_value_t *v)
{
    uint64_t bits = (uint64_t)v->w1 | ((uint64_t)v->w2 << 32);
    double d;
    memcpy(&d, &bits, sizeof(d));
    return d;
}

static inline void mp_value_set_i64(mp_value_t *v, int64_t n)
{
    uint64_t bits;
    memcpy(&bits, &n, sizeof(bits));
    v->w1 = (uint32_t)bits;
    v->w2 = (uint32_t)(bits >> 32);
}

static inline int64_t mp_value_get_i64(const mp_value_t *v)
{
    uint64_t bits = (uint64_t)v->w1 | ((uint64_t)v->w2 << 32);
    int64_t n;
    memcpy(&n, &bits, sizeof(n));
    return n;
}

// vstr storage belongs to MicroPython's GC heap. Values returned to the host
// must instead use the module allocator so the host can release them via free.
static void value_take_vstr(mp_value_t *out, uint32_t kind, vstr_t *text)
{
    char *copy = NULL;
    if (text->len != 0)
    {
        copy = malloc(text->len);
        if (copy == NULL)
        {
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

typedef struct
{
    uint32_t len;
    char blob[];
} object_info_t;

// Object values keep both a guest ref and a printable description.  The ref
// occupies w1; w2 is an aligned object_info_t pointer with its low bits used
// for attributes, leaving the description length in the sidecar header.
static void value_take_object(mp_value_t *out, mp_obj_t obj, uint32_t attributes, vstr_t *text)
{
    object_info_t *info = malloc(sizeof(*info) + text->len);
    if (info == NULL)
    {
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

// Containers serialize their contents, so a container reachable from itself
// would recurse forever. value_path holds the ones being serialized right now;
// a container that reappears crosses as a ref instead of by value. Nesting
// deeper than the path is left untracked and caught by the stack check.
#define VALUE_MAX_DEPTH 128

static mp_obj_t value_path[VALUE_MAX_DEPTH];
static size_t value_depth;

static bool value_enter(mp_obj_t obj)
{
    for (size_t i = 0; i < value_depth && i < VALUE_MAX_DEPTH; i++)
    {
        if (value_path[i] == obj)
        {
            return false;
        }
    }
    if (value_depth < VALUE_MAX_DEPTH)
    {
        value_path[value_depth] = obj;
    }
    ++value_depth;
    return true;
}

static void value_leave(void) { --value_depth; }

static const char *cycle_repr(mp_obj_t obj)
{
    if (mp_obj_is_type(obj, &mp_type_list))
        return "[...]";
    if (mp_obj_is_type(obj, &mp_type_dict))
        return "{...}";
    return "(...)";
}

static void value_from_obj_inner(mp_obj_t obj, mp_value_t *out);

static mp_value_t *serialize_sequence(mp_obj_t seq, uint32_t *out_len)
{
    size_t len;
    mp_obj_t *items;
    mp_obj_get_array(seq, &len, &items);
    *out_len = (uint32_t)len;

    if (len == 0)
        return NULL;

    mp_value_t *buf = malloc(len * sizeof(mp_value_t));
    if (!buf)
        return NULL;

    for (size_t i = 0; i < len; i++)
    {
        value_from_obj_inner(items[i], &buf[i]);
    }
    return buf;
}

static mp_value_t *serialize_dict(mp_obj_t dict_in, uint32_t *out_used)
{
    mp_obj_dict_t *dict = MP_OBJ_TO_PTR(dict_in);
    uint32_t used = dict->map.used;
    *out_used = used;

    if (used == 0)
        return NULL;

    // Allocate 2 mp_value_t structs per key/value pair
    mp_value_t *buf = malloc(used * 2 * sizeof(mp_value_t));
    if (!buf)
        return NULL;

    size_t idx = 0;
    for (size_t i = 0; i < dict->map.alloc; i++)
    {
        if (mp_map_slot_is_filled(&dict->map, i))
        {
            value_from_obj_inner(dict->map.table[i].key, &buf[idx++]);
            value_from_obj_inner(dict->map.table[i].value, &buf[idx++]);
        }
    }
    return buf;
}

static mp_value_t *serialize_set(mp_obj_t set, uint32_t *out_len)
{
    size_t len = (size_t)MP_OBJ_SMALL_INT_VALUE(mp_obj_len(set));
    *out_len = (uint32_t)len;
    if (len == 0)
        return NULL;

    mp_value_t *buf = malloc(len * sizeof(mp_value_t));
    if (!buf)
        return NULL;

    mp_obj_iter_buf_t iter_buf;
    mp_obj_t iter = mp_getiter(set, &iter_buf);
    mp_obj_t item;
    size_t i = 0;
    while ((item = mp_iternext(iter)) != MP_OBJ_STOP_ITERATION)
    {
        value_from_obj_inner(item, &buf[i++]);
    }
    return buf;
}

// A repr of NULL is taken from the object itself. A cyclic container supplies
// its own, since printing it would recurse the same way serializing it does.
static void value_from_object_repr(mp_value_t *out, mp_obj_t obj, uint32_t attributes, const char *repr)
{
    const char *type = mp_obj_get_type_str(obj);
    vstr_t text;
    mp_print_t print;
    vstr_init_print(&text, 64, &print);
    vstr_add_str(&text, type);
    vstr_add_char(&text, '\x04');
    if (repr != NULL)
    {
        vstr_add_str(&text, repr);
    }
    else
    {
        mp_obj_print_helper(&print, obj, PRINT_REPR);
    }
    value_take_object(out, obj, attributes, &text);
}

static void value_from_object(mp_value_t *out, mp_obj_t obj, uint32_t attributes)
{
    value_from_object_repr(out, obj, attributes, NULL);
}

static mp_obj_t exception_from_value(const mp_value_t *in)
{
    const char *blob = (const char *)(uintptr_t)in->w2;
    const char *sep = memchr(blob, '\x04', in->w1);
    size_t type_len = sep == NULL ? 0 : (size_t)(sep - blob);
    const char *message = sep == NULL ? blob : sep + 1;
    size_t message_len = sep == NULL ? in->w1 : in->w1 - type_len - 1;

    const mp_obj_type_t *type = &mp_type_HostError;
    if (type_len > 0)
    {
        qstr name = qstr_from_strn(blob, type_len);
        mp_map_elem_t *elem = mp_map_lookup(
            (mp_map_t *)&mp_module_builtins_globals.map,
            MP_OBJ_NEW_QSTR(name), MP_MAP_LOOKUP);
        if (elem != NULL && mp_obj_is_type(elem->value, &mp_type_type) && mp_obj_is_subclass_fast(elem->value, MP_OBJ_FROM_PTR(&mp_type_BaseException)))
        {
            type = MP_OBJ_TO_PTR(elem->value);
        }
    }

    mp_obj_t msg = mp_obj_new_str(message, message_len);
    free((void *)(uintptr_t)in->w2);
    return mp_obj_new_exception_arg1(type, msg);
}

// Entry point for a value crossing to the host. The path is rewound here
// rather than unwound by hand, since a raise anywhere below skips the pops.
void value_from_obj(mp_obj_t obj, mp_value_t *out)
{
    value_depth = 0;
    value_from_obj_inner(obj, out);
}

static void value_from_obj_inner(mp_obj_t obj, mp_value_t *out)
{
    mp_cstack_check();

    out->w1 = 0;
    out->w2 = 0;

    if (obj == MP_OBJ_NULL)
    {
        out->kind = KIND_NULL;
        return;
    }
    if (obj == mp_const_none)
    {
        out->kind = KIND_NONE;
        return;
    }

    if (obj == mp_const_true || obj == mp_const_false)
    {
        out->kind = KIND_BOOL;
        out->w1 = (obj == mp_const_true);
        return;
    }

    if (mp_obj_is_small_int(obj))
    {
        int64_t v = (int64_t)MP_OBJ_SMALL_INT_VALUE(obj);
        out->kind = KIND_INT;
        mp_value_set_i64(out, v);
        return;
    }

    if (mp_obj_is_int(obj))
    {
        long long v = mp_obj_get_ll(obj);
        if (mp_obj_equal(mp_obj_new_int_from_ll(v), obj))
        {
            out->kind = KIND_INT;
            mp_value_set_i64(out, (int64_t)v);
        }
        else
        {
            value_from_object(out, obj, 0);
        }
        return;
    }

#if MICROPY_PY_BUILTINS_FLOAT
    if (mp_obj_is_float(obj))
    {
        out->kind = KIND_FLOAT;
        mp_value_set_f64(out, (double)mp_obj_get_float(obj));
        return;
    }
#endif

    if (mp_obj_is_str(obj) || mp_obj_is_type(obj, &mp_type_bytes))
    {
        size_t len;
        const char *s = mp_obj_str_get_data(obj, &len);
        out->kind = mp_obj_is_str(obj) ? KIND_STR : KIND_BYTES;
        out->w1 = (uint32_t)len;
        out->w2 = (uintptr_t)s;
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_tuple) || mp_obj_is_type(obj, &mp_type_list))
    {
        if (!value_enter(obj))
        {
            value_from_object_repr(out, obj, 0, cycle_repr(obj));
            return;
        }
        uint32_t len = 0;
        mp_value_t *buf = serialize_sequence(obj, &len);
        value_leave();
        out->kind = mp_obj_is_type(obj, &mp_type_tuple) ? KIND_TUPLE : KIND_LIST;
        out->w1 = len;
        out->w2 = (uintptr_t)buf;
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_dict))
    {
        if (!value_enter(obj))
        {
            value_from_object_repr(out, obj, 0, cycle_repr(obj));
            return;
        }
        uint32_t used = 0;
        mp_value_t *buf = serialize_dict(obj, &used);
        value_leave();
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
    )
    {
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

    uint32_t obj_attributes = 0;

    if (obj_is_iterator(obj))
        obj_attributes |= KIND_OBJECT_ATTR_ITERABLE;

    if (mp_obj_is_callable(obj))
        obj_attributes |= KIND_OBJECT_ATTR_CALLABLE;

    // NOTE: This ensures the object is added to our global refs table
    value_from_object(out, obj, obj_attributes);
}

void value_from_exception(mp_obj_t exc, mp_value_t *out)
{
    static const char fallback[] = "<unprintable exception>";
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0)
    {
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
    }
    else
    {
        out->kind = KIND_EXCEPTION;
        out->w1 = sizeof(fallback) - 1;
        char *copy = malloc(sizeof(fallback) - 1);
        if (copy == NULL)
        {
            out->w1 = 0;
            out->w2 = 0;
        }
        else
        {
            memcpy(copy, fallback, sizeof(fallback) - 1);
            out->w2 = (uintptr_t)copy;
        }
    }
}

mp_obj_t obj_from_value(const mp_value_t *in)
{
    switch ((int32_t)in->kind)
    {
    case KIND_NULL:
        return MP_OBJ_NULL;
    case KIND_NONE:
        return mp_const_none;
    case KIND_BOOL:
        return mp_obj_new_bool(in->w1);
    case KIND_INT:
    {
        return mp_obj_new_int_from_ll(mp_value_get_i64(in));
    }
    case KIND_BIGINT:
    {
        mp_obj_t value = mp_parse_num_integer((const char *)(uintptr_t)in->w2, in->w1, 10, NULL);
        free((void *)(uintptr_t)in->w2);
        return value;
    }
#if MICROPY_PY_BUILTINS_FLOAT
    case KIND_FLOAT:
        return mp_obj_new_float_from_d(mp_value_get_f64(in));
#endif
    case KIND_STR:
    {
        mp_obj_t value = mp_obj_new_str((const char *)(uintptr_t)in->w2, in->w1);
        free((void *)(uintptr_t)in->w2);
        return value;
    }
    case KIND_BYTES:
    {
        mp_obj_t value = mp_obj_new_bytes((const byte *)(uintptr_t)in->w2, in->w1);
        free((void *)(uintptr_t)in->w2);
        return value;
    }
    case KIND_EXCEPTION:
        return exception_from_value(in);
    case KIND_REF:
    case KIND_OBJECT:
    {
        mp_obj_t o = ref_get(in->w1);
        if (o == MP_OBJ_NULL)
        {
            mp_raise_ValueError(MP_ERROR_TEXT("stale ref"));
        }
        return o;
    }
    case KIND_LIST:
    case KIND_TUPLE:
    case KIND_SET:
    case KIND_FROZENSET:
    {
        uint32_t len = in->w1;
        mp_value_t *buf = (mp_value_t *)(uintptr_t)in->w2;

        mp_obj_t seq = mp_obj_new_list(0, NULL);
        if (len > 0 && buf != NULL)
        {
            for (uint32_t i = 0; i < len; i++)
            {
                mp_obj_list_append(seq, obj_from_value(&buf[i]));
            }
            free(buf);
        }

        if (in->kind == KIND_TUPLE)
        {
            seq = mp_obj_new_tuple(len, ((mp_obj_list_t *)MP_OBJ_TO_PTR(seq))->items);
        }
        else if (in->kind == KIND_SET || in->kind == KIND_FROZENSET)
        {
            seq = mp_obj_new_set(len, ((mp_obj_list_t *)MP_OBJ_TO_PTR(seq))->items);
#if MICROPY_PY_BUILTINS_FROZENSET
            if (in->kind == KIND_FROZENSET)
            {
                seq = mp_call_function_1(MP_OBJ_FROM_PTR(&mp_type_frozenset), seq);
            }
#endif
        }
        return seq;
    }
    case KIND_DICT:
    {
        uint32_t num_pairs = in->w1;
        mp_value_t *buf = (mp_value_t *)(uintptr_t)in->w2;
        mp_obj_t dict = mp_obj_new_dict(num_pairs);

        if (num_pairs > 0 && buf != NULL)
        {
            for (uint32_t i = 0; i < num_pairs; i++)
            {
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
