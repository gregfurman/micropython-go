#include <stdint.h>
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
#include "py/cstack.h"

#include "types.h"

#include "port/micropython_embed.h"

// NOTE: HostError is a subclass of RuntimeError
MP_DEFINE_EXCEPTION(HostError, RuntimeError)

__attribute__((import_module("env"), import_name("host_trampoline"))) extern void host_trampoline(uint32_t func_id, uint32_t args_ptr, uint32_t num_args, uint32_t out_ptr);

__attribute__((import_module("env"), import_name("host_stdout"))) extern void host_stdout(uint32_t ptr, uint32_t len);

__attribute__((import_module("env"), import_name("host_poll"))) extern int32_t host_poll(void);

void minimal_vm_poll(void)
{
    if (host_poll())
    {
        mp_raise_type(&mp_type_KeyboardInterrupt);
    }
}

static char *minimal_stack_top;

void gc_helper_collect_regs_and_stack(void)
{
    void *dummy;
    gc_collect_root(&dummy,
                    ((uintptr_t)minimal_stack_top - (uintptr_t)&dummy) / sizeof(uintptr_t));
}

mp_uint_t mp_hal_stdout_tx_strn(const char *str, size_t len)
{
    host_stdout((uint32_t)(uintptr_t)str, (uint32_t)len);
    return len;
}

static int max_host_args;

__attribute__((export_name("init_vm")))
int32_t
init_vm(size_t heap_size, int max_args)
{
    max_host_args = max_args;
    char *heap = (char *)malloc(heap_size);

    if (heap == NULL)
    {
        return -1;
    }

    int stack_top;
    mp_embed_init(heap, heap_size, &stack_top);
    minimal_stack_top = (char *)&stack_top;
    mp_cstack_init_with_top(minimal_stack_top, MICROPY_C_STACK_SIZE);

    refs_reset();
    mp_obj_dict_store(MP_OBJ_FROM_PTR(mp_globals_get()),
                      MP_OBJ_NEW_QSTR(MP_QSTR_HostError),
                      MP_OBJ_FROM_PTR(&mp_type_HostError));
    return 0;
}

static mp_obj_t generic_host_invoke(size_t n_args, const mp_obj_t *args)
{
    uint32_t func_id = (uint32_t)mp_obj_get_int(args[0]);
    size_t n = n_args - 1;
    if (n > (size_t)max_host_args)
    {
        mp_raise_ValueError(MP_ERROR_TEXT("too many args"));
    }

    mp_value_t argbuf[max_host_args];
    mp_value_t ret = {KIND_INVALID, 0, 0};

    for (size_t i = 0; i < n; i++)
    {
        value_from_obj(args[i + 1], &argbuf[i]);
    }

    host_trampoline(func_id, (uint32_t)(uintptr_t)argbuf, (uint32_t)n,
                    (uint32_t)(uintptr_t)&ret);

    if (ret.kind == KIND_INVALID)
    {
        mp_raise_msg(&mp_type_RuntimeError, MP_ERROR_TEXT("host wrote no value"));
    }

    if (ret.kind == KIND_EXCEPTION)
    {
        nlr_raise(obj_from_value(&ret));
    }

    return obj_from_value(&ret);
}

MP_DEFINE_CONST_FUN_OBJ_VAR(generic_host_invoke_obj, 1, generic_host_invoke);

static mp_obj_t new_host_function(uint32_t func_id)
{
    mp_obj_t bound_id = mp_obj_new_int(func_id);
    return mp_obj_new_bound_meth((mp_obj_t)&generic_host_invoke_obj, bound_id);
}

// Return the module at path, creating every prefix and linking each child into
// its parent. mp_obj_new_module also registers the fully-qualified module in
// sys.modules, so ordinary imports find the same objects.
static mp_obj_t get_or_create_module(const char *path, size_t path_len)
{
    if (path_len == 0)
    {
        mp_raise_ValueError(MP_ERROR_TEXT("empty module name"));
    }

    mp_obj_t parent = MP_OBJ_NULL;
    size_t part_start = 0;
    for (size_t end = 0; end <= path_len; ++end)
    {
        if (end != path_len && path[end] != '.')
        {
            continue;
        }
        if (end == part_start)
        {
            mp_raise_ValueError(MP_ERROR_TEXT("invalid module name"));
        }

        mp_obj_t module = mp_obj_new_module(qstr_from_strn(path, end));
        if (!mp_obj_is_type(module, &mp_type_module))
        {
            mp_raise_TypeError(MP_ERROR_TEXT("module name is already occupied"));
        }

        if (parent != MP_OBJ_NULL)
        {
            mp_obj_module_t *parent_module = MP_OBJ_TO_PTR(parent);
            qstr part_name = qstr_from_strn(path + part_start, end - part_start);
            mp_obj_dict_store(MP_OBJ_FROM_PTR(parent_module->globals), MP_OBJ_NEW_QSTR(part_name), module);

            // A parent containing a child is a package. This matters for
            // `from parent import child` and for ports with external imports.
            qstr path_name = qstr_from_strn("__path__", 8);
            if (mp_map_lookup(&parent_module->globals->map, MP_OBJ_NEW_QSTR(path_name), MP_MAP_LOOKUP) == NULL)
            {
                mp_obj_dict_store(MP_OBJ_FROM_PTR(parent_module->globals), MP_OBJ_NEW_QSTR(path_name), mp_obj_new_list(0, NULL));
            }
        }
        parent = module;
        part_start = end + 1;
    }
    return parent;
}

static void module_store_attr(mp_obj_t module, const char *name, size_t name_len, mp_obj_t value)
{
    mp_obj_module_t *mod = MP_OBJ_TO_PTR(module);
    mp_obj_dict_store(MP_OBJ_FROM_PTR(mod->globals), MP_OBJ_NEW_QSTR(qstr_from_strn(name, name_len)), value);
}

__attribute__((export_name("define_function"))) void define_function(const char *name, uint32_t func_id)
{
    mp_obj_t bound_func = new_host_function(func_id);
    qstr q_name = qstr_from_str(name);

    // Fetch the __main__ global dictionary and store the bound function
    mp_obj_dict_store(
        MP_OBJ_FROM_PTR(mp_globals_get()),
        MP_OBJ_NEW_QSTR(q_name),
        bound_func);
}

__attribute__((export_name("define_module"))) void define_module_ext(const char *path, uint32_t path_len, uint32_t out_ptr)
{
    mp_value_t *out = (mp_value_t *)(uintptr_t)out_ptr;
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0)
    {
        get_or_create_module(path, path_len);
        nlr_pop();
        value_from_obj(mp_const_none, out);
    }
    else
    {
        value_from_exception((mp_obj_t)nlr.ret_val, out);
    }
}

__attribute__((export_name("define_module_function"))) void define_module_function_ext(
    const char *path, uint32_t path_len, const char *name, uint32_t name_len, uint32_t func_id, uint32_t out_ptr)
{
    mp_value_t *out = (mp_value_t *)(uintptr_t)out_ptr;
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0)
    {
        module_store_attr(get_or_create_module(path, path_len), name, name_len, new_host_function(func_id));
        nlr_pop();
        value_from_obj(mp_const_none, out);
    }
    else
    {
        value_from_exception((mp_obj_t)nlr.ret_val, out);
    }
}

__attribute__((export_name("set_module_attr"))) void set_module_attr_ext(
    const char *path, uint32_t path_len, const char *name, uint32_t name_len, uint32_t value_ptr, uint32_t out_ptr)
{
    mp_value_t *out = (mp_value_t *)(uintptr_t)out_ptr;
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0)
    {
        module_store_attr(get_or_create_module(path, path_len), name, name_len,
                          obj_from_value((mp_value_t *)(uintptr_t)value_ptr));
        nlr_pop();
        value_from_obj(mp_const_none, out);
    }
    else
    {
        value_from_exception((mp_obj_t)nlr.ret_val, out);
    }
}

// Re-read an object the host holds a ref to. A container comes back by value,
// one level deep: anything cyclic inside it is a ref again.
__attribute__((export_name("ref_to_value"))) void ref_to_value(uint32_t ref, uint32_t out_ptr)
{
    mp_value_t *out = (mp_value_t *)(uintptr_t)out_ptr;
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0)
    {
        mp_obj_t obj = ref_get(ref);
        if (obj == MP_OBJ_NULL)
        {
            mp_raise_ValueError(MP_ERROR_TEXT("stale ref"));
        }
        value_from_obj(obj, out);
        nlr_pop();
    }
    else
    {
        value_from_exception((mp_obj_t)nlr.ret_val, out);
    }
}

// ----

static void execute_python(const char *src, size_t len, mp_parse_input_kind_t input_kind, mp_value_t *out)
{
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0)
    {
        mp_lexer_t *lex = mp_lexer_new_from_str_len(MP_QSTR__lt_string_gt_, src, len, 0);
        qstr source_name = lex->source_name;
        mp_parse_tree_t parse_tree = mp_parse(lex, input_kind);
        mp_obj_t module_fun = mp_compile(&parse_tree, source_name, false);
        mp_obj_t result = mp_call_function_0(module_fun);
        value_from_obj(result, out);
        nlr_pop();
    }
    else
    {
        value_from_exception((mp_obj_t)nlr.ret_val, out);
    }
}

mp_lexer_t *mp_lexer_new_from_file(qstr filename)
{
    mp_raise_OSError(MP_ENOENT);
}

__attribute__((export_name("eval"))) void eval_ext(const char *code, uint32_t len, uint32_t out_ptr)
{
    execute_python(code, len, MP_PARSE_EVAL_INPUT, (mp_value_t *)(uintptr_t)out_ptr);
}

__attribute__((export_name("exec"))) void exec_ext(const char *code, uint32_t len, uint32_t out_ptr)
{
    execute_python(code, len, MP_PARSE_FILE_INPUT, (mp_value_t *)(uintptr_t)out_ptr);
}

__attribute__((export_name("call"))) void call_ext(const char *name, uint32_t name_len, uint32_t args_ptr,
                                                   uint32_t num_args, uint32_t out_ptr)
{
    mp_value_t *out = (mp_value_t *)(uintptr_t)out_ptr;
    mp_value_t *args = (mp_value_t *)(uintptr_t)args_ptr;

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0)
    {
        mp_obj_t fn = mp_load_global(qstr_from_strn(name, name_len));
        if (!mp_obj_is_callable(fn))
        {
            mp_raise_TypeError(MP_ERROR_TEXT("not callable"));
        }
        mp_obj_t argv[num_args];
        for (uint32_t i = 0; i < num_args; i++)
        {
            argv[i] = obj_from_value(&args[i]);
        }
        mp_obj_t result = mp_call_function_n_kw(fn, num_args, 0, argv);
        value_from_obj(result, out);
        nlr_pop();
    }
    else
    {
        value_from_exception((mp_obj_t)nlr.ret_val, out);
    }
}

__attribute__((export_name("call_ref"))) void call_ref_ext(uint32_t ref, uint32_t args_ptr,
                                                           uint32_t num_args, uint32_t out_ptr)
{
    mp_value_t *out = (mp_value_t *)(uintptr_t)out_ptr;
    mp_value_t *args = (mp_value_t *)(uintptr_t)args_ptr;

    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0)
    {
        mp_obj_t fn = ref_get(ref);
        if (fn == MP_OBJ_NULL)
        {
            mp_raise_ValueError(MP_ERROR_TEXT("stale ref"));
        }
        if (!mp_obj_is_callable(fn))
        {
            mp_raise_TypeError(MP_ERROR_TEXT("not callable"));
        }
        mp_obj_t argv[num_args];
        for (uint32_t i = 0; i < num_args; i++)
        {
            argv[i] = obj_from_value(&args[i]);
        }
        mp_obj_t result = mp_call_function_n_kw(fn, num_args, 0, argv);
        value_from_obj(result, out);
        nlr_pop();
    }
    else
    {
        value_from_exception((mp_obj_t)nlr.ret_val, out);
    }
}

__attribute__((export_name("release_ref"))) void release_ref_ext(uint32_t ref)
{
    refs_free(ref);
}

// A restored memory snapshot contains the old host root lists, but Go handles
// from that timeline are intentionally invalid.  Drop those roots so the
// restored guest can collect the no-longer-host-owned objects.
__attribute__((export_name("reset_refs"))) void reset_refs_ext(void)
{
    refs_reset();
}

// Advance a host-held generator.  mp_iternext converts StopIteration to the
// MP_OBJ_STOP_ITERATION sentinel; other Python exceptions cross the ABI as an
// exception value.  A separate status return keeps a yielded None distinct
// from normal exhaustion.
__attribute__((export_name("iterator_next")))
int32_t
iterator_next(uint32_t ref, mp_value_t *out)
{
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0)
    {
        mp_obj_t iter = ref_get(ref);
        if (iter == MP_OBJ_NULL)
        {
            mp_raise_ValueError(MP_ERROR_TEXT("stale ref"));
        }

        mp_obj_t item = mp_iternext(iter);
        if (item == MP_OBJ_STOP_ITERATION)
        {
            nlr_pop();
            return 0;
        }

        value_from_obj(item, out);
        nlr_pop();
        return 1;
    }

    value_from_exception((mp_obj_t)nlr.ret_val, out);
    return -1;
}

__attribute__((export_name("get_global"))) void get_global_ext(const char *name, uint32_t name_len, uint32_t out_ptr)
{
    mp_value_t *out = (mp_value_t *)(uintptr_t)out_ptr;
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0)
    {
        mp_obj_t value = mp_load_global(qstr_from_strn(name, name_len));
        value_from_obj(value, out);
        nlr_pop();
    }
    else
    {
        value_from_exception((mp_obj_t)nlr.ret_val, out);
    }
}

__attribute__((export_name("set_global"))) void set_global_ext(const char *name, uint32_t name_len, uint32_t value_ptr,
                                                               uint32_t out_ptr)
{
    mp_value_t *out = (mp_value_t *)(uintptr_t)out_ptr;
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0)
    {
        mp_obj_t value = obj_from_value((mp_value_t *)(uintptr_t)value_ptr);
        mp_store_global(qstr_from_strn(name, name_len), value);
        nlr_pop();
        out->kind = KIND_NONE;
        out->w1 = 0;
        out->w2 = 0;
    }
    else
    {
        value_from_exception((mp_obj_t)nlr.ret_val, out);
    }
}

mp_import_stat_t mp_import_stat(const char *path)
{
    (void)path;
    return MP_IMPORT_STAT_NO_EXIST;
}

mp_obj_t mp_builtin_open(size_t n_args, const mp_obj_t *args, mp_map_t *kwargs)
{
    (void)n_args;
    (void)args;
    (void)kwargs;
    mp_raise_OSError(MP_ENOENT);
}
MP_DEFINE_CONST_FUN_OBJ_KW(mp_builtin_open_obj, 1, mp_builtin_open);

void MP_NORETURN __fatal_error(const char *msg)
{
    (void)msg;
    __builtin_trap();
}
