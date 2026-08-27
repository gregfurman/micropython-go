#ifndef TYPES_H
#define TYPES_H

#include <stdint.h>
#include "py/obj.h"

enum {
    KIND_INVALID   = 0,  // never written; catches unwritten buffers
    KIND_NULL      = 1,  //
    KIND_NONE      = 2,  // corresponds to python None 
    KIND_BOOL      = 3,  // w1 = 0|1 (TODO: consider constants for this...)
    KIND_INT       = 4,  // w1 = int32
    KIND_BIGINT    = 5,  // w1 = len, w2 = ptr (decimal ascii)
    KIND_FLOAT     = 6,  // w1..w2 = double
    KIND_STR       = 7,  // w1 = len, w2 = ptr
    KIND_BYTES     = 8,  // w1 = len, w2 = ptr
    KIND_TUPLE     = 9,  // w1 = len, w2 = ptr (3-word elements)
    KIND_LIST      = 10, // w1 = len, w2 = ptr (3-word elements)
    KIND_DICT      = 11, // w1 = used, w2 = ptr (alternating k/v 3-word elements)
    KIND_CALLABLE  = 12, // w1 = ref
    KIND_OBJECT    = 13, // w1 = ref
    KIND_REF       = 14, // host -> guest only: w1 = ref
    KIND_EXCEPTION = 15, // w1 = len, w2 = ptr (formatted traceback)
};

void refs_init(void);
void refs_free(uint32_t id);

typedef struct {
    uint32_t kind;
    uint32_t w1;
    uint32_t w2;
} mp_value_t;

void value_from_obj(mp_obj_t obj, mp_value_t *out);
void value_from_exception(mp_obj_t exc, mp_value_t *out);

mp_obj_t obj_from_value(const mp_value_t *in);

#endif