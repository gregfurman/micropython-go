#include "arena.h"

void* arena_alloc(mp_arena_t* a, uint32_t size) {
    uint32_t offset = (a->offset + 3u) & ~3u;

    if (offset > a->capacity || size > a->capacity - offset) {
        a->overflowed = true;
        return NULL;
    }

    void* ptr = a->base + offset;
    a->offset = offset + size;

    return ptr;
}

void arena_push(mp_arena_t* a, mp_active_t* frame, mp_obj_t obj) {
    frame->obj = obj;
    frame->prev = a->active;
    a->active = frame;
    a->depth++;
}

void arena_pop(mp_arena_t* a, mp_active_t* frame) {
    a->active = frame->prev;
    a->depth--;
}

int output_arena_init(mp_arena_t* arena, uint32_t out_ptr, uint32_t out_capacity) {
    if (out_capacity < sizeof(mp_transfer_t)) {
        return -1;
    }

    arena->base = (uint8_t*)(uintptr_t)out_ptr;
    arena->capacity = out_capacity;

    arena->offset = sizeof(mp_transfer_t);
    mp_transfer_t* transfer = (mp_transfer_t*)arena->base;
    transfer->refs = 0;
    transfer->num_refs = 0;

    arena->active = NULL;
    arena->depth = 0;
    arena->overflowed = false;

    return 0;
}

void output_arena_reset(mp_arena_t* arena) {
    arena->offset = sizeof(mp_transfer_t);
    mp_transfer_t* transfer = (mp_transfer_t*)arena->base;
    transfer->refs = 0;
    transfer->num_refs = 0;
    arena->active = NULL;
    arena->depth = 0;
}

int32_t output_arena_status(const mp_arena_t* arena) {
    if (arena->overflowed) {
        return -2;
    }
    return (int32_t)arena->offset;
}
