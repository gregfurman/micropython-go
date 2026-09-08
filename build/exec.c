#include "exec.h"

#include "py/compile.h"
#include "py/runtime.h"
#include "value.h"

// Compile one fragment of source and run it, returning what the module function
// returned. This raises on the way out; both callers below supply the handler.
static mp_obj_t compile_and_run(const char* src, size_t len, mp_parse_input_kind_t input_kind) {
    mp_lexer_t* lex = mp_lexer_new_from_str_len(MP_QSTR__lt_string_gt_, src, len, 0);
    qstr source_name = lex->source_name;
    mp_parse_tree_t parse_tree = mp_parse(lex, input_kind);
    mp_obj_t module_fun = mp_compile(&parse_tree, source_name, false);
    return mp_call_function_0(module_fun);
}

void eval_python(const char* src, size_t len, mp_arena_t* arena, mp_value_t* out) {
    nlr_buf_t nlr;

    if (nlr_push(&nlr) == 0) {
        mp_obj_t result = compile_and_run(src, len, MP_PARSE_EVAL_INPUT);
        value_from_mp_obj_committed(arena, result, out);
        nlr_pop();
    } else {
        output_arena_reset(arena);
        value_from_mp_exception(arena, (mp_obj_t)nlr.ret_val, out);
    }
}

// A module body evaluates to None, and nothing it built leaves the guest by
// this route: whatever it defined is reached later, by name. So there is no
// result to encode, and no reference can be acquired encoding one. The region
// carries the exception that ended the run, or it carries None.
void exec_python(const char* src, size_t len, mp_arena_t* arena, mp_value_t* out) {
    nlr_buf_t nlr;

    if (nlr_push(&nlr) == 0) {
        compile_and_run(src, len, MP_PARSE_FILE_INPUT);
        mp_value_set_none(out);
        nlr_pop();
    } else {
        output_arena_reset(arena);
        value_from_mp_exception(arena, (mp_obj_t)nlr.ret_val, out);
    }
}
