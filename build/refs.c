#include "refs.h"

#include "host.h"
#include "py/objlist.h"
#include "py/runtime.h"

// The guest half of a host handle, and nothing more. An object the host holds
// is pinned in this root list so the guest collector cannot reclaim it.
//
// Everything else about a reference, which slot it occupies, how many
// acquisitions are outstanding, and which generation the slot is on, belongs to
// the host in internal/host/refs.go. Only the pin has to live here, because a
// table in Go memory is invisible to this collector; the rest is bookkeeping
// that costs guest heap for no reason. Keeping it out also keeps this list the
// only structure whose growth competes with Python allocations.
MP_REGISTER_ROOT_POINTER(mp_obj_t host_refs);

// An id is a slot index in the low bits and the generation the slot had when
// it was handed out in the high bits. The host assigns both; this side only
// needs the index.
#define REF_INDEX_BITS 20
#define REF_INDEX_MASK ((1u << REF_INDEX_BITS) - 1)

static mp_obj_list_t* refs_list(void) { return MP_OBJ_TO_PTR(MP_STATE_VM(host_refs)); }

// Drop the pin on a slot. Safe to call for a slot that was never pinned or has
// already been dropped, which is what makes a duplicate release harmless.
static void ref_unpin(uint32_t id) {
    size_t index = id & REF_INDEX_MASK;
    mp_obj_list_t* l = refs_list();
    if (index == 0 || index >= l->len) {
        return;
    }
    l->items[index] = MP_OBJ_NULL;
}

// Reset the root which keeps values handed to the host alive. This is used
// after restoring a memory snapshot: its host refs belong to the old timeline,
// and the Go handles for them belong to the abandoned reference owner, which
// the host replaces at the same time.
void refs_reset(void) {
    MP_STATE_VM(host_refs) = mp_obj_new_list(0, NULL);
    mp_obj_list_append(MP_STATE_VM(host_refs), MP_OBJ_NULL);  // slot 0 reserved
}

uint32_t ref_add(mp_obj_t obj) {
    int32_t id = go_ref_add((uint32_t)(uintptr_t)obj);
    if (id < 0) {
        mp_raise_msg(&mp_type_MemoryError, MP_ERROR_TEXT("too many host references"));
    }

    size_t index = (uint32_t)id & REF_INDEX_MASK;
    mp_obj_list_t* l = refs_list();
    if (index < l->len && l->items[index] == obj) {
        // The host recognised the address and counted another acquisition
        // against the slot that already pins this object.
        return (uint32_t)id;
    }

    // Growing the list can raise, and the host has already counted the
    // acquisition, so give it back rather than stranding the slot.
    nlr_buf_t nlr;
    if (nlr_push(&nlr) != 0) {
        go_ref_free((uint32_t)id);
        nlr_jump(nlr.ret_val);
    }
    while (refs_list()->len <= index) {
        mp_obj_list_append(MP_STATE_VM(host_refs), MP_OBJ_NULL);
    }
    nlr_pop();

    refs_list()->items[index] = obj;
    return (uint32_t)id;
}

// Resolve an id to the object it pins. The host has already checked that the
// id belongs to this interpreter and this generation, so a miss here means the
// slot was released: callers report a stale ref.
mp_obj_t ref_get(uint32_t id) {
    size_t index = id & REF_INDEX_MASK;
    mp_obj_list_t* l = refs_list();
    if (index == 0 || index >= l->len) {
        return MP_OBJ_NULL;
    }
    return l->items[index];
}

// Give one acquisition back. Used by the guest for references a transfer
// acquired but the decoder never claimed; the host releases its own handles
// through the release_ref export instead, having already done the counting.
void refs_free(uint32_t id) {
    if (go_ref_free(id) <= 0) {
        return;
    }
    ref_unpin(id);
}

void refs_unpin(uint32_t id) { ref_unpin(id); }

bool value_release_refs(uint32_t* refs) {
    bool released = false;
    uint32_t ptr = *refs;
    *refs = 0;
    while (ptr != 0) {
        mp_object_info_t* info = (mp_object_info_t*)(uintptr_t)ptr;
        if (info->ref != 0) {
            refs_free(info->ref);
            info->ref = 0;
            released = true;
        }
        ptr = info->next;
    }
    return released;
}
