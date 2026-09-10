#ifndef EXEC_H
#define EXEC_H

#include <stddef.h>

#include "abi.h"
#include "arena.h"
#include "py/lexer.h"
#include "py/parse.h"

// Evaluate one expression, writing its value, or the exception that ended it,
// into the arena.
void eval_python(const char* src, size_t len, mp_arena_t* arena, mp_value_t* out);

// Run one fragment of source for its effects. Nothing crosses but the exception
// that ended it, if one did.
void exec_python(const char* src, size_t len, mp_arena_t* arena, mp_value_t* out);

#endif
