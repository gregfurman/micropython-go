#ifndef ARENA_H
#define ARENA_H

#include <stdbool.h>
#include <stdint.h>

#include "abi.h"
#include "py/obj.h"

typedef struct mp_active_s {
    mp_obj_t obj;
    struct mp_active_s* prev;
} mp_active_t;

#define MP_VALUE_MAX_DEPTH 128

typedef struct {
    // Borrowed span in Wasm linear memory.
    uint8_t* base;
    uint32_t capacity;
    uint32_t offset;

    // Recursive serializer state.
    mp_active_t* active;
    uint32_t depth;

    // Sticky allocation failure.
    bool overflowed;
} mp_arena_t;

// Bump one aligned block out of the arena. Returns NULL and marks the arena
// overflowed when it does not fit; callers raise MemoryError.
void* arena_alloc(mp_arena_t* a, uint32_t size);

void arena_push(mp_arena_t* a, mp_active_t* frame, mp_obj_t obj);
void arena_pop(mp_arena_t* a, mp_active_t* frame);

int output_arena_init(mp_arena_t* arena, uint32_t out_ptr, uint32_t out_capacity);
void output_arena_reset(mp_arena_t* arena);
int32_t output_arena_status(const mp_arena_t* arena);

#endif
