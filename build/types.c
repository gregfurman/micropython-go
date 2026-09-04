#include <string.h>
#include <stdlib.h>

#include "py/objlist.h"
#include "py/builtin.h"
#include "py/parsenum.h"
#include "py/runtime.h"
#include "py/cstack.h"

#include "types.h"

// objset.c keeps the set object private, so this mirrors its layout.
typedef struct
{
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

// An object crosses as a handle carrying its type and repr. Containers do not
// come through here, since printing one recurses over what the host is about to
// read for itself.
static void value_from_object(mp_value_t *out, mp_obj_t obj, uint32_t attributes)
{
    vstr_t text;
    mp_print_t print;
    vstr_init_print(&text, 64, &print);
    vstr_add_str(&text, mp_obj_get_type_str(obj));
    vstr_add_char(&text, '\x04');
    mp_obj_print_helper(&print, obj, PRINT_REPR);
    value_take_object(out, obj, attributes, &text);
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
    return mp_obj_new_exception_arg1(type, msg);
}

// A container crosses as a handle, walked by the host with seq_item and
// map_next. Copying one out here would have to recurse, and a container
// reachable from itself has no finite copy.
static void value_from_container(mp_value_t *out, mp_obj_t obj, uint32_t kind, uint32_t length)
{
    out->kind = kind;
    out->w1 = length;
    out->w2 = ref_add(obj);
}

void value_from_obj(mp_obj_t obj, mp_value_t *out)
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
        size_t len;
        mp_obj_t *items;
        mp_obj_get_array(obj, &len, &items);
        value_from_container(out, obj,
                             mp_obj_is_type(obj, &mp_type_tuple) ? KIND_TUPLE : KIND_LIST,
                             (uint32_t)len);
        return;
    }

    if (mp_obj_is_type(obj, &mp_type_dict))
    {
        mp_obj_dict_t *dict = MP_OBJ_TO_PTR(obj);
        value_from_container(out, obj, KIND_DICT, (uint32_t)dict->map.used);
        return;
    }

#if MICROPY_PY_BUILTINS_SET
    if (mp_obj_is_type(obj, &mp_type_set)
#if MICROPY_PY_BUILTINS_FROZENSET
        || mp_obj_is_type(obj, &mp_type_frozenset)
#endif
    )
    {
        host_set_t *set = MP_OBJ_TO_PTR(obj);
#if MICROPY_PY_BUILTINS_FROZENSET
        uint32_t kind = mp_obj_is_type(obj, &mp_type_frozenset) ? KIND_FROZENSET : KIND_SET;
#else
        uint32_t kind = KIND_SET;
#endif
        value_from_container(out, obj, kind, (uint32_t)set->set.used);
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

// map_next finds the next entry at or after cursor, writing the key to out[0]
// and the value to out[1], unset for a set. It returns the slot it read, or -1
// when there is none left: the table has holes, and they are not the host's to
// know about.
int32_t map_next(mp_obj_t obj, size_t cursor, mp_value_t *out)
{
    out[1].kind = KIND_NULL;
    out[1].w1 = 0;
    out[1].w2 = 0;

    if (mp_obj_is_type(obj, &mp_type_dict))
    {
        mp_map_t *map = &((mp_obj_dict_t *)MP_OBJ_TO_PTR(obj))->map;
        for (size_t slot = cursor; slot < map->alloc; slot++)
        {
            if (mp_map_slot_is_filled(map, slot))
            {
                value_from_obj(map->table[slot].key, &out[0]);
                value_from_obj(map->table[slot].value, &out[1]);
                return (int32_t)slot;
            }
        }
        return -1;
    }

#if MICROPY_PY_BUILTINS_SET
    mp_set_t *set = &((host_set_t *)MP_OBJ_TO_PTR(obj))->set;
    for (size_t slot = cursor; slot < set->alloc; slot++)
    {
        if (mp_set_slot_is_filled(set, slot))
        {
            value_from_obj(set->table[slot], &out[0]);
            return (int32_t)slot;
        }
    }
#endif
    return -1;
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
        return mp_parse_num_integer((const char *)(uintptr_t)in->w2, in->w1, 10, NULL);
    }
#if MICROPY_PY_BUILTINS_FLOAT
    case KIND_FLOAT:
        return mp_obj_new_float_from_d(mp_value_get_f64(in));
#endif
    case KIND_STR:
    {
        return mp_obj_new_str((const char *)(uintptr_t)in->w2, in->w1);
    }
    case KIND_BYTES:
    {
        return mp_obj_new_bytes((const byte *)(uintptr_t)in->w2, in->w1);
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
        }
        return dict;
    }
    default:
        mp_raise_ValueError(MP_ERROR_TEXT("bad host value"));
    }
}

// value_release frees the buffers behind a value the host wrote. Callers run it
// once the call is over, the raise path included: freeing as the tree is
// converted would leave whatever an unwind skipped to nobody. Guest-written
// values are not released here, since their text points into live objects.
void value_release(const mp_value_t *in)
{
    switch ((int32_t)in->kind)
    {
    case KIND_BIGINT:
    case KIND_STR:
    case KIND_BYTES:
    case KIND_EXCEPTION:
        free((void *)(uintptr_t)in->w2);
        return;

    case KIND_LIST:
    case KIND_TUPLE:
    case KIND_SET:
    case KIND_FROZENSET:
    case KIND_DICT:
    {
        mp_value_t *buf = (mp_value_t *)(uintptr_t)in->w2;
        if (buf == NULL)
        {
            return;
        }
        values_release(buf, in->w1 * (in->kind == KIND_DICT ? 2 : 1));
        free(buf);
        return;
    }

    default:
        return;
    }
}

void values_release(const mp_value_t *buf, uint32_t count)
{
    for (uint32_t i = 0; i < count; i++)
    {
        value_release(&buf[i]);
    }
}
