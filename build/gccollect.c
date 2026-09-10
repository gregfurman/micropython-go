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

// A finished call can leave object pointers in stack slots that a later GC
// scans conservatively. Clear a bounded window after the last host pin is
// removed, from a later frame whose live locals sit above that window.
#define STACK_SCRUB_BYTES 4096

__attribute__((noinline)) void scrub_dead_stack(void) {
    char anchor;
    uintptr_t top = (uintptr_t)&anchor & ~(uintptr_t)3;
    uintptr_t bottom = top - STACK_SCRUB_BYTES;

    if (minimal_stack_limit != NULL && bottom < (uintptr_t)minimal_stack_limit) {
        bottom = ((uintptr_t)minimal_stack_limit + 3u) & ~(uintptr_t)3;
    }

    // Do not call memset: its own frame could lie inside the window. Keep the
    // writes volatile so optimization cannot turn this loop into such a call.
    for (volatile uint32_t* p = (volatile uint32_t*)bottom; p < (volatile uint32_t*)top; p++) {
        *p = 0;
    }
}
