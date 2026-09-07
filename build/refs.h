#ifndef REFS_H
#define REFS_H

#include <stdbool.h>
#include <stdint.h>

#include "abi.h"
#include "py/obj.h"

typedef struct {
    mp_obj_t obj;
    mp_value_t* value;
} pending_ref_t;

// The guest side of a host handle. An object the host holds is pinned in a root
// list here, so the guest collector cannot reclaim something Go still names.

void refs_reset(void);
uint32_t ref_add(mp_obj_t obj);
mp_obj_t ref_get(uint32_t id);
void refs_free(uint32_t id);

// Drop a pin the host has already finished counting down. This is the path the
// host's own queued releases take, so it must not count anything again.
void refs_unpin(uint32_t id);

// Release references the decoder did not claim. Returns whether any were
// released. Always detach the list before its arena is reused.
bool value_release_refs(uint32_t* refs);

#endif
