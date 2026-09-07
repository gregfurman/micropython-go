#ifndef VALUE_H
#define VALUE_H

#include "abi.h"
#include "arena.h"
#include "py/obj.h"

// Conversion between guest objects and the wire records in abi.h. convert.c
// dispatches on type or kind; the value_*.c files hold one type each.

void value_from_obj(mp_arena_t* arena, mp_obj_t obj, mp_value_t* out);
void value_from_exception(mp_arena_t* arena, mp_obj_t exc, mp_value_t* out);

mp_obj_t obj_from_value(const mp_value_t* in);

// Raised for a host failure that does not name a builtin exception class, so
// guest code can tell a failed callback from an interpreter error. Defined by
// MP_DEFINE_EXCEPTION in vm.c and published into globals at init.
extern const mp_obj_type_t mp_type_HostError;

#endif
