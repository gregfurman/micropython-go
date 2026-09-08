#include "vm.h"

#include <stdlib.h>

#include "gccollect.h"
#include "hostfn.h"
#include "port/micropython_embed.h"
#include "py/cstack.h"
#include "py/mpconfig.h"
#include "py/runtime.h"
#include "refs.h"
#include "value.h"

// NOTE: HostError is a subclass of RuntimeError
MP_DEFINE_EXCEPTION(HostError, RuntimeError)

int32_t vm_init(size_t heap_size, int max_args) {
    max_host_args = max_args;
    char* heap = (char*)malloc(heap_size);

    if (heap == NULL) {
        return -1;
    }

    int stack_top;
    mp_embed_init(heap, heap_size, &stack_top);
    gc_collect_init((char*)&stack_top);
    mp_cstack_init_with_top((char*)&stack_top, MICROPY_C_STACK_SIZE);

    refs_reset();
    mp_obj_dict_store(
        MP_OBJ_FROM_PTR(mp_globals_get()), MP_OBJ_NEW_QSTR(MP_QSTR_HostError), MP_OBJ_FROM_PTR(&mp_type_HostError));
    return 0;
}

void vm_enter(void) { MP_STATE_THREAD(gc_lock_depth) = 0; }
