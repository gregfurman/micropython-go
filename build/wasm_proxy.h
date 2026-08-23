// Guest objects the host holds by reference.  See wasm_proxy.c.

#ifndef MICROPY_INCLUDED_WASI_WASM_PROXY_H
#define MICROPY_INCLUDED_WASI_WASM_PROXY_H

#include <stdint.h>

#include "py/obj.h"

#include "wasm_api.h"

// Adds obj to the reference table and returns its index.  Called from the
// value walk (wasm_value.c) for anything the host has no type of its own for.
int32_t mp_api_ref_add(mp_obj_t obj);

// Resolves an index the host sent back, raising if it is not live.  Called
// from the decoder (wasm_build.c) for PK_OBJECT.
mp_obj_t mp_api_ref_get(int32_t ref);

// How many references are held, for host.mem_info().
size_t mp_api_ref_count(void);

// Creates the table; called from the constructor in wasm_api.c.
void mp_api_proxy_init(void);

// Calls a referenced object, and drops every reference at once. Both are the
// host's to drive; see the lifetime note in wasm_proxy.c.
MP_API_EXPORT(mp_api_ref_call) int32_t mp_api_ref_call(int32_t ref, const uint8_t *ptr, uint32_t len, uint32_t n_args);
MP_API_EXPORT(mp_api_refs_clear) void mp_api_refs_clear(void);

#endif // MICROPY_INCLUDED_WASI_WASM_PROXY_H
