#ifndef VM_H
#define VM_H

#include <stddef.h>
#include <stdint.h>

// vm_init allocates the guest heap and starts the interpreter. max_args is the
// argument ceiling the host promises to respect on callbacks. Returns 0, or -1
// if the heap could not be allocated.
int32_t vm_init(size_t heap_size, int max_args);

// vm_enter starts one host call. Python that locks the heap and never unlocks
// it, by raising or by just forgetting, would otherwise leave every later
// allocation failing for the life of the instance. Each host call is the
// top-level the REPL prompt is upstream, so each one clears the lock.
void vm_enter(void);

#endif
