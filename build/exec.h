#ifndef EXEC_H
#define EXEC_H

#include <stddef.h>

#include "abi.h"
#include "arena.h"
#include "py/lexer.h"
#include "py/parse.h"

// Compile and run one fragment of source, writing the result, or the exception
// that ended it, into the arena. input_kind is what separates eval from exec.
void execute_python(
    const char* src, size_t len, mp_parse_input_kind_t input_kind, mp_arena_t* arena, mp_value_t* out);

#endif
