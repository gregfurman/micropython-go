#include "arena.h"

#include "refs.h"

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

// arena_push marks obj as being copied, so anything nested inside it can tell
// that reaching obj again closes a cycle. The frame lives in the caller's own
// stack frame, so tracking costs no allocation.
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
    arena->refs = &((mp_transfer_t*)(uintptr_t)out_ptr)->refs;
    *arena->refs = 0;
    arena->active = NULL;
    arena->depth = 0;
    arena->overflowed = false;
    return 0;
}

// Discard a partly written result and reuse the region for the exception. The
// active list goes with it, since an nlr jump left those frames pointing into
// unwound stack. The overflow flag does not: whether the region was too small
// is exactly what the host still needs to know.
void output_arena_reset(mp_arena_t* arena) {
    value_release_refs(arena->refs);
    arena->offset = sizeof(mp_transfer_t);
    arena->active = NULL;
    arena->depth = 0;
}

// output_arena_status reports the bytes a call used, or -2 if the region could
// not hold the whole value tree. A Python exception is not a failure here: it
// comes back as a KIND_EXCEPTION value with a positive length.
int32_t output_arena_status(const mp_arena_t* arena) {
    if (arena->overflowed) {
        return -2;
    }
    return (int32_t)arena->offset;
}
