#ifndef TYPES_H
#define TYPES_H

#include <stdbool.h>
#include <stdint.h>

#include "py/obj.h"

enum {
    KIND_INVALID = 0,  // never written; catches unwritten buffers
    KIND_NULL = 1,     //
    KIND_NONE = 2,     // corresponds to python None
    KIND_BOOL = 3,     // w1 = 0|1 (TODO: consider constants for this...)
    KIND_INT = 4,      // w1..w2 = int64
    KIND_BIGINT = 5,   // w1 = len, w2 = ptr (decimal ascii)
    KIND_FLOAT = 6,    // w1..w2 = double
    KIND_STR = 7,      // w1 = len, w2 = ptr
    KIND_BYTES = 8,    // w1 = len, w2 = ptr
    // Containers cross in opposite shapes: out as a handle the host walks,
    // in as one block the host laid out.
    // guest -> host: w1 = len, w2 = ref;
    // host -> guest: w1 = len, w2 = ptr (3-word elements)
    KIND_TUPLE = 9,
    KIND_LIST = 10,
    KIND_DICT = 11,  // host -> guest: w1 = pairs, w2 = ptr (alternating k/v)
    // guest -> host: w1 = ref, w2 = object-info ptr | attributes;
    // host -> guest: w1 = ref
    KIND_OBJECT = 13,
    KIND_REF = 14,        // host -> guest only: w1 = ref
    KIND_EXCEPTION = 15,  // w1 = len, w2 = ptr (formatted traceback)
    KIND_SET = 16,        // as KIND_LIST
    KIND_FROZENSET = 17,  // as KIND_LIST
};

typedef enum {
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

typedef struct {
    uint32_t kind;
    uint32_t w1;
    uint32_t w2;
} mp_value_t;

_Static_assert(sizeof(mp_value_t) == 12, "mp_value_t ABI mismatch");
_Static_assert(_Alignof(mp_value_t) == 4, "mp_value_t alignment mismatch");

// A container being copied right now. Frames live on the C stack and thread
// through the arena, so a container reachable from itself is recognised on the
// way down without allocating anything to track it.
typedef struct mp_active_s {
    mp_obj_t obj;
    struct mp_active_s* prev;
} mp_active_t;

// MP_VALUE_MAX_DEPTH bounds how far a copied value tree nests. Past it a
// container crosses as a handle instead, the same way a cycle does, so deep
// nesting costs the host another call rather than an error. It matches
// maxDecodeDepth on the Go side, which must not be the tighter of the two.
#define MP_VALUE_MAX_DEPTH 128

typedef struct {
    uint8_t* base;
    uint32_t capacity;
    uint32_t offset;
    // Containers between the root and the value being written.
    mp_active_t* active;
    // How many of them there are.
    uint32_t depth;
    // Set once a payload does not fit. Sticky: a tree with a hole in it is not
    // a result, however the call ends, so the host is told the region was too
    // small rather than handed a truncated value.
    bool overflowed;
} mp_arena_t;

// iterator_next returns 1 after writing an item to out, 0 when the iterator
// is exhausted, and -1 after writing an exception to out.
int32_t iterator_next(uint32_t ref, mp_value_t* out);

void value_from_obj(mp_arena_t* arena, mp_obj_t obj, mp_value_t* out);
void value_from_exception(mp_arena_t* arena, mp_obj_t exc, mp_value_t* out);

mp_obj_t obj_from_value(const mp_value_t* in);

#endif
