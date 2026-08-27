#include <stdint.h>
#include <string.h>
#include <stdlib.h>

#include "py/mpconfig.h" 
#include "py/obj.h"
#include "py/runtime.h"
#include "py/compile.h"
#include "py/parse.h"
#include "py/lexer.h"
#include "py/qstr.h"
#include "py/builtin.h"
#include "py/mperrno.h"
#include "py/mphal.h"
#include "py/gc.h"

#include "types.h"

#include "port/micropython_embed.h"

MP_DEFINE_EXCEPTION(HostError, Exception)

extern void refs_init(void);

__attribute__((import_module("env"), import_name("host_trampoline")))
extern void host_trampoline(uint32_t func_id, uint32_t args_ptr, uint32_t num_args, uint32_t out_ptr);

// TODO: hack to get stdout
__attribute__((import_module("env"), import_name("host_stdout")))
extern void host_stdout(uint32_t ptr, uint32_t len);

mp_uint_t mp_hal_stdout_tx_strn(const char *str, size_t len) {
    host_stdout((uint32_t)(uintptr_t)str, (uint32_t)len);
    return len;
}

char *heap;
int max_host_args;

__attribute__((export_name("init_vm")))
void init_vm(size_t heap_size, int max_args) {
    max_host_args = max_args;
    heap = (char *)malloc(heap_size);
    
    if (heap == NULL) {
        return; 
    }

    int stack_top;
    mp_embed_init(heap, heap_size, &stack_top);
    
    refs_init();
    mp_obj_dict_store(MP_OBJ_FROM_PTR(mp_globals_get()),
                      MP_OBJ_NEW_QSTR(MP_QSTR_HostError),
                      MP_OBJ_FROM_PTR(&mp_type_HostError));
}

static mp_obj_t generic_host_invoke(size_t n_args, const mp_obj_t *args) {
    uint32_t func_id = (uint32_t)mp_obj_get_int(args[0]);
    size_t n = n_args - 1;
    if (n > max_host_args) {
        mp_raise_ValueError(MP_ERROR_TEXT("too many args"));
    }

    mp_value_t argbuf[max_host_args];
    mp_value_t ret = { KIND_INVALID, 0, 0 };

    for (size_t i = 0; i < n; i++) {
        value_from_obj(args[i + 1], &argbuf[i]);
    }

    host_trampoline(func_id, (uint32_t)(uintptr_t)argbuf, (uint32_t)n,
                    (uint32_t)(uintptr_t)&ret);

    if (ret.kind == KIND_INVALID) {
        mp_raise_msg(&mp_type_RuntimeError, MP_ERROR_TEXT("host wrote no value"));
    }

    if (ret.kind == KIND_EXCEPTION) {
        mp_obj_t msg = mp_obj_new_str((const char *)(uintptr_t)ret.w2, ret.w1);
        free((void *)(uintptr_t)ret.w2);
        // TODO: if a specific error is raised, perhaps use that type?
        // e.g if a PythonError is returned then it should be parsed
        // and a new exception populated.
        nlr_raise(mp_obj_new_exception_arg1(&mp_type_HostError, msg));
    }

    mp_obj_t res = obj_from_value(&ret);
    if (ret.kind == KIND_STR || ret.kind == KIND_BYTES || ret.kind == KIND_BIGINT) {
        free((void *)(uintptr_t)ret.w2);
    }
    return res;
}

MP_DEFINE_CONST_FUN_OBJ_VAR(generic_host_invoke_obj, 1, generic_host_invoke);

__attribute__((export_name("define_function")))
void define_function(const char *name, uint32_t func_id) {
    mp_obj_t bound_id = mp_obj_new_int(func_id);
    mp_obj_t bound_func = mp_obj_new_bound_meth((mp_obj_t)&generic_host_invoke_obj, bound_id);
    qstr q_name = qstr_from_str(name);
    
    // Fetch the __main__ global dictionary and store the bound function
    mp_obj_dict_store(
        MP_OBJ_FROM_PTR(mp_globals_get()),
        MP_OBJ_NEW_QSTR(q_name),
        bound_func
    );
}

__attribute__((export_name("obj_to_value")))
void obj_to_value(uint32_t obj_in, uint32_t out_ptr) {
    mp_obj_t obj = (mp_obj_t)(uintptr_t)obj_in;
    mp_value_t *out = (mp_value_t *)(uintptr_t)out_ptr;
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        value_from_obj(obj, out);
        nlr_pop();
    } else {
        value_from_exception((mp_obj_t)nlr.ret_val, out);
    }
}

__attribute__((export_name("kind_of")))
int32_t kind_of(uint32_t i) {
    static const int32_t k[] = {
        KIND_EXCEPTION, KIND_NULL, KIND_NONE, KIND_BOOL, KIND_INT,
        KIND_BIGINT, KIND_FLOAT, KIND_STR, KIND_BYTES,
        KIND_CALLABLE, KIND_OBJECT, KIND_REF,
    };
    return i < sizeof(k) / sizeof(k[0]) ? k[i] : INT32_MIN;
}

// ----

static void execute_python(const char *src, size_t len, mp_parse_input_kind_t input_kind, mp_value_t *out) {
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        mp_lexer_t *lex = mp_lexer_new_from_str_len(MP_QSTR__lt_stdin_gt_, src, len, 0);
        qstr source_name = lex->source_name;
        mp_parse_tree_t parse_tree = mp_parse(lex, input_kind);
        mp_obj_t module_fun = mp_compile(&parse_tree, source_name, false);
        mp_obj_t result = mp_call_function_0(module_fun);
        nlr_pop();
        value_from_obj(result, out);
    } else {
        value_from_exception((mp_obj_t)nlr.ret_val, out);
    }
}

mp_lexer_t *mp_lexer_new_from_file(qstr filename) {
    mp_raise_OSError(MP_ENOENT);
}

__attribute__((export_name("eval")))
void eval_ext(const char *code, uint32_t len, uint32_t out_ptr) {
    execute_python(code, len, MP_PARSE_EVAL_INPUT, (mp_value_t *)(uintptr_t)out_ptr);
}

__attribute__((export_name("exec")))
void exec_ext(const char *code) {
    mp_embed_exec_str(code);
}

mp_import_stat_t mp_import_stat(const char *path) {
    (void)path;
    return MP_IMPORT_STAT_NO_EXIST;
}

mp_obj_t mp_builtin_open(size_t n_args, const mp_obj_t *args, mp_map_t *kwargs) {
    (void)n_args; (void)args; (void)kwargs;
    mp_raise_OSError(MP_ENOENT);
}
MP_DEFINE_CONST_FUN_OBJ_KW(mp_builtin_open_obj, 1, mp_builtin_open);

void MP_NORETURN __fatal_error(const char *msg) {
    (void)msg;
    __builtin_trap();
}