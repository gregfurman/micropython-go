#ifndef GCCOLLECT_H
#define GCCOLLECT_H

// The C stack, from the collector's point of view. MicroPython scans it
// conservatively, so both functions here are about which words it sees.

// Record where the stack starts. Called once from vm_init, with the address of
// a local in the frame the interpreter was started from.
void gc_collect_init(char* stack_top);

// Clear a window of dead stack below the caller, so a pointer a finished call
// left behind cannot keep an unreferenced object alive. See the definition for
// why only a later, shallower call may do this.
// void scrub_dead_stack(void);

#endif
