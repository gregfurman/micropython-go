#ifndef ABI_H
#define ABI_H

#include <stdbool.h>
#include <stdint.h>
#include <string.h>

// The record every value crosses the boundary as. kind decides what w1 and w2
// mean; nothing else about the layout ever varies. The host mirrors this in
// internal/host/codec.

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
    // Containers are copied, not handed over: w1 = len, w2 = ptr to that many
    // mp_value_t in the same arena. The same shape both ways. A container that
    // cannot be copied, one reaching itself or one past MP_VALUE_MAX_DEPTH,
    // crosses as KIND_OBJECT instead.
    KIND_TUPLE = 9,
    KIND_LIST = 10,
    KIND_DICT = 11,  // w1 = pairs, w2 = ptr to w1 * 2 values (alternating k/v)
    // guest -> host: w1 = ref, w2 = mp_object_class_t ptr | attributes;
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

typedef struct {
    uint32_t kind;
    uint32_t w1;
    uint32_t w2;
} mp_value_t;

_Static_assert(sizeof(mp_value_t) == 12, "mp_value_t ABI mismatch");
_Static_assert(_Alignof(mp_value_t) == 4, "mp_value_t alignment mismatch");

// A handle's Python class, written into the arena beside the record naming it.
// The host cannot ask for this later without a call per handle, and the name is
// a qstr the guest already holds, so it crosses with the handle instead.
//
// w2 carries the pointer with the attribute flags folded into its low bits,
// which the arena's four-byte alignment always leaves clear.
typedef struct {
    uint32_t len;
    char name[];
} mp_object_class_t;

_Static_assert(sizeof(mp_object_class_t) == 4, "object class ABI mismatch");
_Static_assert(_Alignof(mp_object_class_t) == 4, "object class alignment mismatch");
_Static_assert(KIND_OBJECT_ATTR_MASK < 4, "attributes must fit the pointer's spare bits");

// The host releases every acquisition in refs after decoding this result.
// Decoded Go handles retain their own acquisitions; records are read-only.
typedef struct {
    mp_value_t value;
    uint32_t refs;
    uint32_t num_refs;
} mp_transfer_t;

_Static_assert(sizeof(mp_transfer_t) == 20, "transfer ABI mismatch");

// What a call with no result to describe writes. The region still has to hold
// a readable value, since the host decodes every result it is handed.
static inline void mp_value_set_none(mp_value_t* v) {
    v->kind = KIND_NONE;
    v->w1 = 0;
    v->w2 = 0;
}

// A 64-bit payload occupies both words, low half first. memcpy rather than a
// union, so nothing here depends on the guest's alignment for a double.

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

#endif
