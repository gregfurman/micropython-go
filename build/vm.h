#ifndef VM_H
#define VM_H

#include <stddef.h>
#include <stdint.h>

// vm_init allocates the guest heap and starts the interpreter. max_args is the
// argument ceiling the host promises to respect on callbacks. Returns 0, or -1
// if the heap could not be allocated.
int32_t vm_init(size_t heap_size, int max_args);

#endif
