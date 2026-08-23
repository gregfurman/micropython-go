/*
 * Go functions the guest can call, and host values bound into its globals.
 *
 * Both directions already exist here, so a host call is not a new wire format,
 * only a new order of the two.  Going out reuses the val_* callbacks
 * (wasm_value.c): the host cannot read an mp_obj_t out of memory, so arguments
 * are walked and streamed the same way a return value is.  Coming back is the
 * packed format (wasm_build.c), and that direction really is just a memory
 * address -- the host writes the reply into a buffer this file owns and returns
 * a pointer to it:
 *
 *     [u32 length][length bytes of packed value]
 *
 * There is no status beside that value, because the value already says: a
 * failure arrives as PK_EXCEPTION, which is a kind like any other.  A null
 * pointer means the host had nothing at all to say.
 *
 * A host function is its own object type, on the same shape ports/webassembly
 * gives a JavaScript object: mp_obj_jsproxy_t is a proxy holding an int ref
 * into a table on the other side, and its attr slot answers nothing itself --
 * it forwards the lookup and lets the other side decide.  This does the same,
 * which is why __name__ and __doc__ appear nowhere in this file.  Metadata
 * belongs to whoever has it, and synthesising it here would fix in C what the
 * host can answer, and change, on its own.
 *
 * A builtin bound to a small int -- mp_obj_new_bound_meth -- calls just as
 * well and costs nothing to define, but it reprs as `<bound_method>` and has
 * no attr slot at all, so a guest printing one or reaching it in a traceback
 * learns nothing about what it is.
 */

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "py/builtin.h"
#include "py/objtuple.h"
#include "py/runtime.h"

#include "wasm_api.h"
#include "wasm_pack.h"
#include "wasm_proxy.h"

#define HOST_IMPORT(name) __attribute__((import_module("host"), import_name(#name)))

// Declared in wasm_value.c too; both are the same import.
HOST_IMPORT(val_tuple) extern void host_val_tuple(uint32_t len);
HOST_IMPORT(val_dict) extern void host_val_dict(uint32_t len);

// Opens a call.  The arguments follow through the val_* callbacks, as one
// (args, kwargs) pair.
//
// It has to be announced, rather than inferred from the arguments arriving,
// because they arrive *into* a decode the host may already have in progress:
// a __repr__ running while a return value is walked can reach a host function
// halfway through a container.
HOST_IMPORT(call_begin) extern void host_call_begin(void);

// Runs host function id and returns its reply, or NULL.
HOST_IMPORT(call_end) extern const uint8_t *host_call_end(int32_t id);

// Reads an attribute of host function id, in the same reply format.  NULL
// means there is no such attribute, which is not an error: it is what makes
// the guest raise AttributeError, and what getattr's default and hasattr are
// built on.
HOST_IMPORT(attr) extern const uint8_t *host_attr(int32_t id, const char *name, uint32_t name_len);


// Where the host writes that reply.  Its own buffer rather than the argument
// scratch, so a reply can never land on top of something still being read.
static uint8_t *reply_buf;
static size_t reply_cap;

void *mp_api_reply(uint32_t size) {
    if (size > reply_cap) {
        uint8_t *grown = realloc(reply_buf, size);
        if (grown == NULL) {
            return NULL;
        }
        reply_buf = grown;
        reply_cap = size;
    }
    return reply_buf;
}

typedef struct _hostfunc_obj_t {
    mp_obj_base_t base;
    // Only so the repr can name it without a round trip.  Everything else
    // about the function is the host's to answer.
    qstr name;
    int32_t id;
} hostfunc_obj_t;

static void hostfunc_print(const mp_print_t *print, mp_obj_t self_in, mp_print_kind_t kind) {
    (void)kind;
    hostfunc_obj_t *self = MP_OBJ_TO_PTR(self_in);
    mp_printf(print, "<host function %q>", self->name);
}

// Turns a reply into a value, raising it instead if that is what it is.
static mp_obj_t unpack_reply(const uint8_t *reply) {
    uint32_t len;
    memcpy(&len, reply, sizeof(len));

    const uint8_t *packed = reply + sizeof(len);

    // Read before unpacking rather than after: once decoded, an exception the
    // host raised and an exception it returned as a value look the same.
    bool raised = len > 0 && packed[0] == PK_EXCEPTION;

    // Unpacked where it lies: the reply is in a malloc'd buffer, not the GC
    // heap, so nothing the unpacking allocates can move it.
    mp_obj_t value = mp_api_unpack(packed, len);

    if (raised) {
        nlr_raise(value);
    }
    return value;
}

// Attribute reads go to the host.  Stores fall through with dest[0] left as
// it was, which is how a type says its attributes are read-only.
static void hostfunc_attr(mp_obj_t self_in, qstr attr, mp_obj_t *dest) {
    if (dest[0] != MP_OBJ_NULL) {
        return;
    }

    hostfunc_obj_t *self = MP_OBJ_TO_PTR(self_in);
    size_t len;
    const byte *name = qstr_data(attr, &len);

    const uint8_t *reply = host_attr(self->id, (const char *)name, len);
    if (reply != NULL) {
        dest[0] = unpack_reply(reply);
    }
}

// Keywords arrive interleaved after the positional arguments, in the same
// key, value order a dict is streamed in.
static mp_obj_t hostfunc_call(mp_obj_t self_in, size_t n_args, size_t n_kw, const mp_obj_t *args) {
    hostfunc_obj_t *self = MP_OBJ_TO_PTR(self_in);
    mp_cstack_check();

    host_call_begin();

    host_val_tuple(2);

    host_val_tuple(n_args);
    for (size_t i = 0; i < n_args; i++) {
        mp_api_emit_value(args[i]);
    }

    host_val_dict(n_kw);
    for (size_t i = 0; i < 2 * n_kw; i++) {
        mp_api_emit_value(args[n_args + i]);
    }

    const uint8_t *reply = host_call_end(self->id);
    if (reply == NULL) {
        mp_raise_msg(&mp_type_RuntimeError, MP_ERROR_TEXT("host call failed"));
    }
    return unpack_reply(reply);
}
static MP_DEFINE_CONST_OBJ_TYPE(
    hostfunc_type,
    MP_QSTR_hostfunc,
    MP_TYPE_FLAG_NONE,
    print, hostfunc_print,
    call, hostfunc_call,
    attr, hostfunc_attr
    );

// --- exports ---------------------------------------------------------------

// Binds id to a global of the given name.  Called once per function before the
// host takes its snapshot, so every interpreter restored from that snapshot
// has the same names bound to the same ids.
int32_t mp_api_register(const char *name, uint32_t name_len, int32_t id) {
    MP_API_ENTER();

    hostfunc_obj_t *fn = mp_obj_malloc(hostfunc_obj_t, &hostfunc_type);
    fn->name = qstr_from_strn(name, name_len);
    fn->id = id;
    mp_store_global(fn->name, MP_OBJ_FROM_PTR(fn));

    MP_API_LEAVE();
}

// Binds any packed value to a global name, which is the same thing one level
// down: a host function is a global that happens to be callable.
int32_t mp_api_set(const char *name, uint32_t name_len, const uint8_t *ptr, uint32_t len) {
    MP_API_ENTER();

    mp_obj_t value = mp_api_unpack(ptr, len);
    mp_store_global(qstr_from_strn(name, name_len), value);

    MP_API_LEAVE();
}
