#include "exec.h"

#include "py/compile.h"
#include "py/runtime.h"
#include "value.h"

void execute_python(
    const char* src, size_t len, mp_parse_input_kind_t input_kind, mp_arena_t* arena, mp_value_t* out) {
    nlr_buf_t nlr;
    if (nlr_push(&nlr) == 0) {
        mp_lexer_t* lex = mp_lexer_new_from_str_len(MP_QSTR__lt_string_gt_, src, len, 0);
        qstr source_name = lex->source_name;
        mp_parse_tree_t parse_tree = mp_parse(lex, input_kind);
        mp_obj_t module_fun = mp_compile(&parse_tree, source_name, false);
        mp_obj_t result = mp_call_function_0(module_fun);
        value_from_obj(arena, result, out);
        nlr_pop();
    } else {
        output_arena_reset(arena);
        value_from_exception(arena, (mp_obj_t)nlr.ret_val, out);
    }
}
