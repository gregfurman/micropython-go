#include "hostfn.h"

#include "arena.h"
#include "host.h"
#include "py/runtime.h"
#include "refs.h"
#include "value.h"

int max_host_args;

// Host callbacks consume their arguments synchronously, so the encoded
// value tree only needs to live for the duration of host_trampoline.
// This scratch arena holds payloads referenced by the top-level argbuf
// records, including strings and nested containers.
#define HOST_CALLBACK_ARENA_CAPACITY (16 * 1024)

static mp_obj_t generic_host_invoke(size_t n_args, const mp_obj_t* args) {
    uint32_t func_id = (uint32_t)mp_obj_get_int(args[0]);
    size_t n = n_args - 1;

    if (n > (size_t)max_host_args) mp_raise_ValueError(MP_ERROR_TEXT("too many args"));
    _Alignas(4) uint8_t arg_storage[HOST_CALLBACK_ARENA_CAPACITY];
    uint32_t refs = 0;

    mp_arena_t arg_arena = {
        .base = arg_storage,
        .capacity = sizeof(arg_storage),
        .offset = 0,
        .refs = &refs,
    };

    mp_value_t argbuf[max_host_args];

    _Alignas(4) uint8_t ret_storage[HOST_CALLBACK_ARENA_CAPACITY];

    mp_value_t* ret = (mp_value_t*)ret_storage;

    ret->kind = KIND_INVALID;
    ret->w1 = 0;
    ret->w2 = 0;

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        for (size_t i = 0; i < n; i++) {
            value_from_obj(&arg_arena, args[i + 1], &argbuf[i]);
        }
        host_trampoline(
            func_id, (uint32_t)(uintptr_t)argbuf, (uint32_t)n, (uint32_t)(uintptr_t)ret_storage, sizeof(ret_storage));
        nlr_pop();
    } else {
        value_release_refs(&refs);
        nlr_raise(nlr.ret_val);
    }
    value_release_refs(&refs);

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
