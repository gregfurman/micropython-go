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
#include "pymodule.h"
#include "refs.h"
#include "value.h"
#include "vm.h"

__attribute__((export_name("init_vm"))) int32_t init_vm(size_t heap_size, int max_args) {
    return vm_init(heap_size, max_args);
}

__attribute__((export_name("define_function"))) void define_function(const char* name, uint32_t func_id) {
    mp_obj_t bound_func = new_host_function(func_id);
    qstr q_name = qstr_from_str(name);

    // Fetch the __main__ global dictionary and store the bound function
    mp_obj_dict_store(MP_OBJ_FROM_PTR(mp_globals_get()), MP_OBJ_NEW_QSTR(q_name), bound_func);
}

__attribute__((export_name("define_module"))) int32_t define_module_ext(
    const char* path, uint32_t path_len, uint32_t out_ptr, uint32_t out_capacity) {
    mp_value_t* out = (mp_value_t*)(uintptr_t)out_ptr;
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        get_or_create_module(path, path_len);
        value_from_obj_committed(&arena, mp_const_none, out);
        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }
    return output_arena_status(&arena);
}

__attribute__((export_name("define_module_function"))) int32_t define_module_function_ext(const char* path,
    uint32_t path_len,
    const char* name,
    uint32_t name_len,
    uint32_t func_id,
    uint32_t out_ptr,
    uint32_t out_capacity) {
    mp_value_t* out = (mp_value_t*)(uintptr_t)out_ptr;
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        module_store_attr(get_or_create_module(path, path_len), name, name_len, new_host_function(func_id));
        value_from_obj_committed(&arena, mp_const_none, out);
        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }
    return output_arena_status(&arena);
}

__attribute__((export_name("set_module_attr"))) int32_t set_module_attr_ext(const char* path,
    uint32_t path_len,
    const char* name,
    uint32_t name_len,
    uint32_t value_ptr,
    uint32_t out_ptr,
    uint32_t out_capacity) {
    mp_value_t* out = (mp_value_t*)(uintptr_t)out_ptr;
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        module_store_attr(
            get_or_create_module(path, path_len), name, name_len, obj_from_value((mp_value_t*)(uintptr_t)value_ptr));
        value_from_obj_committed(&arena, mp_const_none, out);
        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }

    return output_arena_status(&arena);
}

// Re-read an object the host holds a ref to. A container comes back by value,
// one level deep: anything cyclic inside it is a ref again.
__attribute__((export_name("ref_to_value"))) int32_t ref_to_value(
    uint32_t ref, uint32_t out_ptr, uint32_t out_capacity) {
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
        value_from_obj_committed(&arena, obj, out);
        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }
    return output_arena_status(&arena);
}

__attribute__((export_name("eval"))) int32_t eval_ext(
    const char* code, uint32_t len, uint32_t out_ptr, uint32_t out_capacity) {
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }
    execute_python(code, len, MP_PARSE_EVAL_INPUT, &arena, (mp_value_t*)(uintptr_t)out_ptr);
    return output_arena_status(&arena);
}

__attribute__((export_name("exec"))) int32_t exec_ext(
    const char* code, uint32_t len, uint32_t out_ptr, uint32_t out_capacity) {
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }
    execute_python(code, len, MP_PARSE_FILE_INPUT, &arena, (mp_value_t*)(uintptr_t)out_ptr);
    return output_arena_status(&arena);
}

__attribute__((export_name("call"))) int32_t call_ext(const char* name,
    uint32_t name_len,
    uint32_t args_ptr,
    uint32_t num_args,
    uint32_t out_ptr,
    uint32_t out_capacity) {
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
        value_from_obj_committed(&arena, result, out);

        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }
    return output_arena_status(&arena);
}

__attribute__((export_name("call_ref"))) int32_t call_ref_ext(
    uint32_t ref, uint32_t args_ptr, uint32_t num_args, uint32_t out_ptr, uint32_t out_capacity) {
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
        value_from_obj_committed(&arena, result, out);
        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }

    return output_arena_status(&arena);
}

// Dropping the last host reference to an object is the one moment stale stack
// matters: until then the ref table kept the object alive anyway, and after it
// only a leftover pointer can. Scrubbing here rather than on every call keeps
// the cost off the hot path, where it roughly doubled a trivial call.
__attribute__((export_name("release_ref"))) void release_ref_ext(uint32_t ref) {
    refs_unpin(ref);
    scrub_dead_stack();
}

// A restored memory snapshot contains the old host root lists, but Go handles
// from that timeline are intentionally invalid.  Drop those roots so the
// restored guest can collect the no-longer-host-owned objects.
__attribute__((export_name("reset_refs"))) void reset_refs_ext(void) { refs_reset(); }

// Advance a host-held generator. This returns what every other result-producing
// export returns, the bytes used or a negative failure, and an uncaught Python
// exception crosses as a KIND_EXCEPTION value the same way.
//
// Exhaustion is the one outcome with no result to describe. mp_iternext turns
// StopIteration into the MP_OBJ_STOP_ITERATION sentinel, which crosses as 0: a
// yielded None is a value like any other, and 0 is not a size any result can
// have, since every one opens with a transfer header.
__attribute__((export_name("iterator_next"))) int32_t iterator_next_ext(
    uint32_t ref, uint32_t out_ptr, uint32_t out_capacity) {
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

        value_from_obj_committed(&arena, item, out);
        nlr_pop();
        return output_arena_status(&arena);
    }

    output_arena_reset(&arena);
    value_from_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    return output_arena_status(&arena);
}

__attribute__((export_name("get_global"))) int32_t get_global_ext(
    const char* name, uint32_t name_len, uint32_t out_ptr, uint32_t out_capacity) {
    mp_value_t* out = (mp_value_t*)(uintptr_t)out_ptr;
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        mp_obj_t value = mp_load_global(qstr_from_strn(name, name_len));
        value_from_obj_committed(&arena, value, out);
        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }
    return output_arena_status(&arena);
}

__attribute__((export_name("set_global"))) int32_t set_global_ext(
    const char* name, uint32_t name_len, uint32_t value_ptr, uint32_t out_ptr, uint32_t out_capacity) {
    mp_value_t* out = (mp_value_t*)(uintptr_t)out_ptr;
    mp_arena_t arena;
    if (output_arena_init(&arena, out_ptr, out_capacity) != 0) {
        return -1;
    }

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        mp_obj_t value = obj_from_value((mp_value_t*)(uintptr_t)value_ptr);
        mp_store_global(qstr_from_strn(name, name_len), value);
        value_from_obj_committed(&arena, mp_const_none, out);
        nlr_pop();
    } else {
        output_arena_reset(&arena);
        value_from_exception(&arena, (mp_obj_t)nlr.ret_val, out);
    }

    return output_arena_status(&arena);
}
