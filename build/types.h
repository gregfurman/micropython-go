#ifndef TYPES_H
#define TYPES_H

#include <stdint.h>
#include "py/obj.h"

enum
{
    KIND_INVALID = 0, // never written; catches unwritten buffers
    KIND_NULL = 1,    //
    KIND_NONE = 2,    // corresponds to python None
    KIND_BOOL = 3,    // w1 = 0|1 (TODO: consider constants for this...)
    KIND_INT = 4,     // w1..w2 = int64
    KIND_BIGINT = 5,  // w1 = len, w2 = ptr (decimal ascii)
    KIND_FLOAT = 6,   // w1..w2 = double
    KIND_STR = 7,     // w1 = len, w2 = ptr
    KIND_BYTES = 8,   // w1 = len, w2 = ptr
    // Containers cross in opposite shapes: out as a handle the host walks,
    // in as one block the host laid out.
    // guest -> host: w1 = len, w2 = ref;
    // host -> guest: w1 = len, w2 = ptr (3-word elements)
    KIND_TUPLE = 9,
    KIND_LIST = 10,
    KIND_DICT = 11, // host -> guest: w1 = pairs, w2 = ptr (alternating k/v)
    // guest -> host: w1 = ref, w2 = object-info ptr | attributes;
    // host -> guest: w1 = ref
    KIND_OBJECT = 13,
    KIND_REF = 14,       // host -> guest only: w1 = ref
    KIND_EXCEPTION = 15, // w1 = len, w2 = ptr (formatted traceback)
    KIND_SET = 16,       // as KIND_LIST
    KIND_FROZENSET = 17, // as KIND_LIST
};

typedef enum
{
    KIND_OBJECT_ATTR_ITERABLE = 1u << 0,
    KIND_OBJECT_ATTR_CALLABLE = 1u << 1,
} KindObjectAttributeFlags;

#define KIND_OBJECT_ATTR_MASK (KIND_OBJECT_ATTR_ITERABLE | KIND_OBJECT_ATTR_CALLABLE)

// Raised for a host failure that does not name a builtin exception class, so
// guest code can tell a failed callback from an interpreter error. Defined by
// MP_DEFINE_EXCEPTION in main.c.
extern const mp_obj_type_t mp_type_HostError;

void refs_reset(void);
uint32_t ref_add(mp_obj_t obj);
mp_obj_t ref_get(uint32_t id);
void refs_free(uint32_t id);

typedef struct
{
    uint32_t kind;
    uint32_t w1;
    uint32_t w2;
} mp_value_t;

// iterator_next returns 1 after writing an item to out, 0 when the iterator
// is exhausted, and -1 after writing an exception to out.
int32_t iterator_next(uint32_t ref, mp_value_t *out);

void value_from_obj(mp_obj_t obj, mp_value_t *out);
void value_from_exception(mp_obj_t exc, mp_value_t *out);

int32_t map_next(mp_obj_t obj, size_t cursor, mp_value_t *out);

mp_obj_t obj_from_value(const mp_value_t *in);
void value_release(const mp_value_t *in);
void values_release(const mp_value_t *buf, uint32_t count);

#endif
