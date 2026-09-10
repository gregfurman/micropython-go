#ifndef HOSTFN_H
#define HOSTFN_H

#include <stdint.h>

#include "py/obj.h"

// A Go function published into the guest. Calling one encodes its arguments
// into a stack arena, hands them to the host trampoline, and decodes whatever
// the host wrote back, raising it if it is an exception.

// The argument ceiling the host agreed on at init_vm. Read by every callback,
// so it bounds the stack arrays those callbacks declare.
extern int max_host_args;

// Wrap func_id as a callable object, ready to be bound into globals or a module.
mp_obj_t new_host_function(uint32_t func_id);

#endif
