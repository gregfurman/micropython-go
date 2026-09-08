#include "hostfn.h"

#include "arena.h"
#include "host.h"
#include "py/runtime.h"
#include "value.h"

int max_host_args;

// Host callbacks consume their arguments synchronously, so the encoded
// value tree only needs to live for the duration of host_trampoline.
// This scratch arena holds the argument tuple, payloads and reference ledger.
#define HOST_CALLBACK_ARENA_CAPACITY (16 * 1024)

static mp_obj_t generic_host_invoke(size_t n_args, const mp_obj_t* args) {
    uint32_t func_id = (uint32_t)mp_obj_get_int(args[0]);
    size_t n = n_args - 1;

    if (n > (size_t)max_host_args) mp_raise_ValueError(MP_ERROR_TEXT("too many args"));
    _Alignas(4) uint8_t arg_storage[HOST_CALLBACK_ARENA_CAPACITY];

    mp_arena_t arg_arena;
    output_arena_init(&arg_arena, (uint32_t)(uintptr_t)arg_storage, sizeof(arg_storage));
    mp_value_t* argbuf = arena_alloc(&arg_arena, n * sizeof(mp_value_t));
    if (argbuf == NULL) {
        mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("arena full"));
    }
    mp_transfer_t* arguments = (mp_transfer_t*)arg_storage;
    arguments->value = (mp_value_t){
        .kind = KIND_TUPLE,
        .w1 = n,
        .w2 = (uint32_t)(uintptr_t)argbuf,
    };
    // Account for the tuple wrapper when bounding nested argument trees.
    arg_arena.depth = 1;

    _Alignas(4) uint8_t ret_storage[HOST_CALLBACK_ARENA_CAPACITY];

    mp_value_t* ret = (mp_value_t*)ret_storage;

    ret->kind = KIND_INVALID;
    ret->w1 = 0;
    ret->w2 = 0;

    // NOTE: args are committed prior to trampoline
    value_from_mp_objs_committed(&arg_arena, &args[1], n, argbuf);
    host_trampoline(func_id,
        (uint32_t)(uintptr_t)arg_storage,
        arg_arena.offset,
        (uint32_t)(uintptr_t)ret_storage,
        sizeof(ret_storage));

    if (ret->kind == KIND_INVALID) {
        mp_raise_msg(&mp_type_RuntimeError, MP_ERROR_TEXT("host wrote no value"));
    }

    mp_obj_t result = obj_from_value(ret);
    if (ret->kind == KIND_EXCEPTION) {
        nlr_raise(result);
    }

    return result;
}
MP_DEFINE_CONST_FUN_OBJ_VAR(generic_host_invoke_obj, 1, generic_host_invoke);

mp_obj_t new_host_function(uint32_t func_id) {
    mp_obj_t bound_id = mp_obj_new_int(func_id);
    return mp_obj_new_bound_meth((mp_obj_t)&generic_host_invoke_obj, bound_id);
}
