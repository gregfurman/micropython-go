#ifndef ARENA_H
#define ARENA_H

#include <stdbool.h>
#include <stdint.h>

#include "abi.h"
#include "py/obj.h"

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
    uint32_t* refs;
    // Containers between the root and the value being written.
    mp_active_t* active;
    // How many of them there are.
    uint32_t depth;
    // Set once a payload does not fit. Sticky: a tree with a hole in it is not
    // a result, however the call ends, so the host is told the region was too
    // small rather than handed a truncated value.
    bool overflowed;
} mp_arena_t;

// Bump one aligned block out of the arena. Returns NULL and marks the arena
// overflowed when it does not fit; callers raise MemoryError.
void* arena_alloc(mp_arena_t* a, uint32_t size);

void arena_push(mp_arena_t* a, mp_active_t* frame, mp_obj_t obj);
void arena_pop(mp_arena_t* a, mp_active_t* frame);

// The output arena covers a region the host reserved for one call's result. It
// opens with an mp_transfer_t: the root value, then the acquisition list head.
int output_arena_init(mp_arena_t* arena, uint32_t out_ptr, uint32_t out_capacity);
void output_arena_reset(mp_arena_t* arena);
int32_t output_arena_status(const mp_arena_t* arena);

#endif
