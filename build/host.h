#ifndef HOST_H
#define HOST_H

#include <stdint.h>

// The three functions the guest imports from the host. Everything else in this
// port is called by the host, not the other way round.

__attribute__((import_module("env"), import_name("host_trampoline"))) extern void host_trampoline(
    uint32_t func_id, uint32_t args_ptr, uint32_t num_args, uint32_t out_ptr, uint32_t out_capacity);

__attribute__((import_module("env"), import_name("host_stdout"))) extern void host_stdout(uint32_t ptr, uint32_t len);

__attribute__((import_module("env"), import_name("host_poll"))) extern int32_t host_poll(void);

// Reference bookkeeping lives on the host: slot allocation, deduplication by
// address, generations and acquisition counts. The guest keeps only the pin
// itself, because nothing in Go memory is traceable by this collector.
//
// go_ref_add takes the object's address and returns its id, or -1 if no slot
// is available. go_ref_free gives one acquisition back and returns 1 when that
// was the last one, meaning the guest should drop the pin.
__attribute__((import_module("env"), import_name("go_ref_add"))) extern int32_t go_ref_add(uint32_t addr);

__attribute__((import_module("env"), import_name("go_ref_free"))) extern int32_t go_ref_free(uint32_t id);

#endif
