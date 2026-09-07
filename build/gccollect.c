#include "gccollect.h"

#include <stdint.h>

#include "py/gc.h"
#include "py/mpconfig.h"

static char* minimal_stack_top;
static char* minimal_stack_limit;

void gc_collect_init(char* stack_top) {
    minimal_stack_top = stack_top;
    minimal_stack_limit = minimal_stack_top - MICROPY_C_STACK_SIZE;
}

void gc_helper_collect_regs_and_stack(void) {
    void* dummy;
    gc_collect_root(&dummy, ((uintptr_t)minimal_stack_top - (uintptr_t)&dummy) / sizeof(uintptr_t));
}

// The collector scans the C stack conservatively, so an mp_obj_t a finished
// call left in a frame can keep a dead object alive: the next call is scanned
// across those same addresses, and its own frames do not necessarily overwrite
// every word of the old ones. The result of a call is the value most likely to
// linger, since it sits in the outermost frames, which the shallowest of the
// following calls never reaches.
//
// A call cannot scrub the frame it is standing in, so this runs from a later,
// shallower call, by which point the frames that held the result are dead and
// lie below it. The window is small because the residue that matters is
// shallow: the outermost frames of the call that produced the value.
#define STACK_SCRUB_BYTES 4096

// Never inlined, so the anchor is this function's own frame. Anchoring in a
// caller would put that caller's locals, whose order the compiler picks, inside
// the window being cleared.
// __attribute__((noinline)) void scrub_dead_stack(void) {
//     char anchor;
//     uintptr_t top = (uintptr_t)&anchor & ~(uintptr_t)3;
//     uintptr_t bottom = top - STACK_SCRUB_BYTES;

//     if (minimal_stack_limit != NULL && bottom < (uintptr_t)minimal_stack_limit) {
//         bottom = ((uintptr_t)minimal_stack_limit + 3u) & ~(uintptr_t)3;
//     }

//     // A word loop rather than memset, which would build a frame inside the
//     // window and could overwrite its own spilled arguments as it went.
//     for (volatile uint32_t* p = (volatile uint32_t*)bottom; p < (volatile uint32_t*)top; p++) {
//         *p = 0;
//     }
// }
