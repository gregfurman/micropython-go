#ifndef PYMODULE_H
#define PYMODULE_H

#include <stddef.h>

#include "py/obj.h"

// Building the module tree a host package is published into. Paths are dotted
// and every prefix is created, so "a.b.c" leaves three linked modules behind.

mp_obj_t get_or_create_module(const char* path, size_t path_len);

void module_store_attr(mp_obj_t module, const char* name, size_t name_len, mp_obj_t value);

#endif
