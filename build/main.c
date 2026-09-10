// Every function the host can call, and nothing else. Each one opens an output
// arena over the region the host reserved, does its work under an nlr handler,
// and returns the bytes used or a negative ABI failure. A Python exception is
// not a failure: it is written into the same region as a KIND_EXCEPTION value.
//
// The subsystems these call live in vm.c, hostfn.c, pymodule.c and exec.c.

#include <stdint.h>

#include "arena.h"
#include "exec.h"
#include "gccollect.h"
#include "hostfn.h"
#include "py/runtime.h"
#include "refs.h"
#include "value.h"
#include "vm.h"

#define EXPORT(ret, fn, name, params, args)            \
    static ret fn##_body params;                       \
    __attribute__((export_name(name))) ret fn params { \
        vm_enter();                                    \
        return fn##_body args;                         \
    }                                                  \
    static ret fn##_body params

// init_vm is the one export that predates the interpreter it would enter.
__attribute__((export_name("init_vm"))) int32_t init_vm(size_t heap_size, int max_args) {
    return vm_init(heap_size, max_args);
}

EXPORT(void, define_function, "define_function", (const char* name, uint32_t func_id), (name, func_id)) {
    mp_obj_t bound_func = new_host_function(func_id);
    qstr q_name = qstr_from_str(name);

    // Fetch the __main__ global dictionary and store the bound function
    mp_obj_dict_store(MP_OBJ_FROM_PTR(mp_globals_get()), MP_OBJ_NEW_QSTR(q_name), bound_func);
}

// Re-read an object the host holds a ref to. A container comes back by value,
// one level deep: anything cyclic inside it is a ref again.
EXPORT(int32_t,
    ref_to_value,
    "ref_to_value",
    (uint32_t ref, uint32_t out_ptr, uint32_t out_capacity),
    (ref, out_ptr, out_capacity)) {
    mp_value_t* out = (mp_value_t*)(uintptr_t)out_ptr;
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        mp_obj_t obj = ref_get(ref);
        if (obj == MP_OBJ_NULL) {
            mp_raise_ValueError(MP_ERROR_TEXT("stale ref"));
        }
        value_from_mp_obj_committed(&arena, obj, out);
        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_mp_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }
    return output_arena_status(&arena);
}

EXPORT(int32_t,
    eval_ext,
    "eval",
    (const char* code, uint32_t len, uint32_t out_ptr, uint32_t out_capacity),
    (code, len, out_ptr, out_capacity)) {
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }
    eval_python(code, len, &arena, (mp_value_t*)(uintptr_t)out_ptr);
    return output_arena_status(&arena);
}

EXPORT(int32_t,
    exec_ext,
    "exec",
    (const char* code, uint32_t len, uint32_t out_ptr, uint32_t out_capacity),
    (code, len, out_ptr, out_capacity)) {
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }
    exec_python(code, len, &arena, (mp_value_t*)(uintptr_t)out_ptr);
    return output_arena_status(&arena);
}

EXPORT(int32_t,
    call_ext,
    "call",
    (const char* name,
        uint32_t name_len,
        uint32_t args_ptr,
        uint32_t num_args,
        uint32_t out_ptr,
        uint32_t out_capacity),
    (name, name_len, args_ptr, num_args, out_ptr, out_capacity)) {
    mp_value_t* out = (mp_value_t*)(uintptr_t)out_ptr;
    const mp_value_t* args = (const mp_value_t*)(uintptr_t)args_ptr;
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        mp_obj_t fn = mp_load_global(qstr_from_strn(name, name_len));

        if (!mp_obj_is_callable(fn)) {
            mp_raise_TypeError(MP_ERROR_TEXT("not callable"));
        }

        mp_obj_t argv[num_args];
        for (uint32_t i = 0; i < num_args; i++) {
            argv[i] = obj_from_value(&args[i]);
        }

        mp_obj_t result = mp_call_function_n_kw(fn, num_args, 0, argv);
        value_from_mp_obj_committed(&arena, result, out);

        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_mp_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }
    return output_arena_status(&arena);
}

EXPORT(int32_t,
    call_ref_ext,
    "call_ref",
    (uint32_t ref, uint32_t args_ptr, uint32_t num_args, uint32_t out_ptr, uint32_t out_capacity),
    (ref, args_ptr, num_args, out_ptr, out_capacity)) {
    mp_value_t* out = (mp_value_t*)(uintptr_t)out_ptr;
    const mp_value_t* args = (const mp_value_t*)(uintptr_t)args_ptr;
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        mp_obj_t fn = ref_get(ref);
        if (fn == MP_OBJ_NULL) {
            mp_raise_ValueError(MP_ERROR_TEXT("stale ref"));
        }
        if (!mp_obj_is_callable(fn)) {
            mp_raise_TypeError(MP_ERROR_TEXT("not callable"));
        }
        mp_obj_t argv[num_args];
        for (uint32_t i = 0; i < num_args; i++) {
            argv[i] = obj_from_value(&args[i]);
        }
        mp_obj_t result = mp_call_function_n_kw(fn, num_args, 0, argv);
        value_from_mp_obj_committed(&arena, result, out);
        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_mp_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }

    return output_arena_status(&arena);
}

// Dropping the last host reference to an object is the one moment stale stack
// matters: until then the ref table kept the object alive anyway, and after it
// only a leftover pointer can. Scrubbing here rather than on every call keeps
// the cost off the hot path, where it roughly doubled a trivial call.
EXPORT(void, release_ref_ext, "release_ref", (uint32_t ref), (ref)) {
    refs_unpin(ref);
    scrub_dead_stack();
}

// A restored memory snapshot contains the old host root lists, but Go handles
// from that timeline are intentionally invalid.  Drop those roots so the
// restored guest can collect the no-longer-host-owned objects.
EXPORT(void, reset_refs_ext, "reset_refs", (void), ()) { refs_reset(); }

// Advance a host-held generator. This returns what every other result-producing
// export returns, the bytes used or a negative failure, and an uncaught Python
// exception crosses as a KIND_EXCEPTION value the same way.
//
// Exhaustion is the one outcome with no result to describe. mp_iternext turns
// StopIteration into the MP_OBJ_STOP_ITERATION sentinel, which crosses as 0: a
// yielded None is a value like any other, and 0 is not a size any result can
// have, since every one opens with a transfer header.
EXPORT(int32_t,
    iterator_next_ext,
    "iterator_next",
    (uint32_t ref, uint32_t out_ptr, uint32_t out_capacity),
    (ref, out_ptr, out_capacity)) {
    mp_value_t* out = (mp_value_t*)(uintptr_t)out_ptr;
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -2;
    }

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        mp_obj_t iter = ref_get(ref);
        if (iter == MP_OBJ_NULL) {
            mp_raise_ValueError(MP_ERROR_TEXT("stale ref"));
        }

        mp_obj_t item = mp_iternext(iter);
        if (item == MP_OBJ_STOP_ITERATION) {
            nlr_pop();
            return 0;
        }

        value_from_mp_obj_committed(&arena, item, out);
        nlr_pop();
        return output_arena_status(&arena);
    }

    output_arena_reset(&arena);
    value_from_mp_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    return output_arena_status(&arena);
}

EXPORT(int32_t,
    get_global_ext,
    "get_global",
    (const char* name, uint32_t name_len, uint32_t out_ptr, uint32_t out_capacity),
    (name, name_len, out_ptr, out_capacity)) {
    mp_value_t* out = (mp_value_t*)(uintptr_t)out_ptr;
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        mp_obj_t value = mp_load_global(qstr_from_strn(name, name_len));
        value_from_mp_obj_committed(&arena, value, out);
        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_mp_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }
    return output_arena_status(&arena);
}

EXPORT(int32_t,
    set_global_ext,
    "set_global",
    (const char* name, uint32_t name_len, uint32_t value_ptr, uint32_t out_ptr, uint32_t out_capacity),
    (name, name_len, value_ptr, out_ptr, out_capacity)) {
    mp_value_t* out = (mp_value_t*)(uintptr_t)out_ptr;
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        mp_obj_t value = obj_from_value((mp_value_t*)(uintptr_t)value_ptr);
        mp_store_global(qstr_from_strn(name, name_len), value);
        mp_value_set_none(out);
        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_mp_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }

    return output_arena_status(&arena);
}
