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

typedef struct {
    uint32_t kind;
    uint32_t w1;
    uint32_t w2;
} mp_value_t;

_Static_assert(sizeof(mp_value_t) == 12, "mp_value_t ABI mismatch");
_Static_assert(_Alignof(mp_value_t) == 4, "mp_value_t alignment mismatch");

// Guest-to-host object metadata belongs to the transfer arena. ref is cleared
// by the decoder when a Go handle takes ownership; next links all acquisitions,
// including objects in a tree whose serialization or decoding fails.
typedef struct {
    uint32_t len;
    uint32_t ref;
    uint32_t next;
    char blob[];
} mp_object_info_t;

// A guest result starts with a value and the head of its acquisition list.
// Callback arguments use the same list but keep its head on the C stack.
typedef struct {
    mp_value_t value;
    uint32_t refs;
} mp_transfer_t;

_Static_assert(sizeof(mp_object_info_t) == 12, "object info ABI mismatch");
_Static_assert(sizeof(mp_transfer_t) == 16, "transfer ABI mismatch");

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
