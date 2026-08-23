/*
 * Guest objects the host holds by reference.
 *
 * Most values cross as copies: an int becomes a Go int64, a dict a Go map.
 * That works because both sides have the type.  For everything else -- a
 * function, a class, an instance -- there is nothing on the other side to copy
 * into, and an mp_obj_t cannot go over as it stands: it is a pointer into the
 * GC heap, and the host has no way to keep it rooted across a collection.
 *
 * So the object stays here and the host gets an index into this table, which
 * is the same answer ports/webassembly reaches with proxy_c_ref.  The table is
 * a plain Python list registered as a GC root, which is what makes the
 * reference safe: the object is reachable for exactly as long as the host
 * might send the index back.
 *
 * Where this differs from proxy_c.c is the lifetime.  There, each reference is
 * released individually from a JavaScript finaliser, so the table needs a
 * free-slot search and slots come and go.  Nothing here can rely on a
 * finaliser, and the host restores a snapshot over this memory between calls --
 * after which an index means a different object, or none at all.  So the
 * lifetime is one call: references only accumulate within it, and the host
 * drops all of them at once when it starts the next.  That is why there is no
 * free list and no per-object release below.
 */

#include <stdint.h>

#include "py/objlist.h"
#include "py/runtime.h"

#include "wasm_api.h"
#include "wasm_proxy.h"

MP_REGISTER_ROOT_POINTER(mp_obj_t mp_api_refs);

void mp_api_proxy_init(void) {
    MP_STATE_VM(mp_api_refs) = mp_obj_new_list(0, NULL);
}

static mp_obj_list_t *refs(void) {
    return MP_OBJ_TO_PTR(MP_STATE_VM(mp_api_refs));
}

int32_t mp_api_ref_add(mp_obj_t obj) {
    mp_obj_list_append(MP_STATE_VM(mp_api_refs), obj);
    return refs()->len - 1;
}

mp_obj_t mp_api_ref_get(int32_t ref) {
    if (ref < 0 || (size_t)ref >= refs()->len) {
        mp_raise_ValueError(MP_ERROR_TEXT("stale object reference"));
    }
    return refs()->items[ref];
}

size_t mp_api_ref_count(void) {
    return refs()->len;
}

void mp_api_refs_clear(void) {
    refs()->len = 0;
}

/*
 * Calling one.
 *
 * This is the one entry point that runs inside another: the host reaches it
 * from a host function, which Python called, which the host called.  So it
 * deliberately does not use MP_API_ENTER.  That records a new C stack top, and
 * gc_collect() scans from the current frame up to it -- lowering it to a nested
 * frame would hide every outer frame's spilled pointers from the collector.
 * The top the outermost call recorded is the right one, and is left alone.
 */
int32_t mp_api_ref_call(int32_t ref, const uint8_t *ptr, uint32_t len, uint32_t n_args) {
    nlr_buf_t nlr;
    if (nlr_push(&nlr) != 0) {
        mp_api_store_error((mp_obj_t)nlr.ret_val);
        return MP_API_ERR;
    }

    mp_obj_t fn = mp_api_ref_get(ref);

    // Decoded above whatever the call this is nested in already has on the
    // stack, and rewound after, so neither disturbs the other.
    size_t mark;
    mp_obj_t *args = mp_api_unpack_args(ptr, len, n_args, &mark);
    mp_obj_t result = mp_call_function_n_kw(fn, n_args, 0, args);
    mp_api_unpack_rewind(mark);

    mp_api_emit_value(result);

    MP_API_LEAVE();
}
