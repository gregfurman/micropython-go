#ifndef REFS_H
#define REFS_H

#include <stdbool.h>
#include <stdint.h>

#include "abi.h"
#include "py/obj.h"

// The guest side of a host handle. An object the host holds is pinned in a root
// list here, so the guest collector cannot reclaim something Go still names.

void refs_reset(void);
uint32_t ref_add(mp_obj_t obj);
mp_obj_t ref_get(uint32_t id);
void ref_release(uint32_t id);

// Drop a pin the host has already finished counting down. This is the path the
// host's own queued releases take, so it must not count anything again.
void refs_unpin(uint32_t id);

#endif
