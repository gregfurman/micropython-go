#ifndef VALUE_H
#define VALUE_H

#include "abi.h"
#include "arena.h"
#include "py/obj.h"

void value_from_mp_obj_committed(mp_arena_t* arena, mp_obj_t obj, mp_value_t* out);
void value_from_mp_objs_committed(mp_arena_t* arena, const mp_obj_t* objs, size_t n, mp_value_t* out);

void value_from_mp_exception(mp_arena_t* arena, mp_obj_t exc, mp_value_t* out);

mp_obj_t obj_from_value(const mp_value_t* in);

extern const mp_obj_type_t mp_type_HostError;

#endif
