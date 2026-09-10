#pragma once

// The libc-gen unistd.h only declares sbrk, but MicroPython reaches here for
// SEEK_* (py/stream.c) and ssize_t (py/runtime.c, py/emitbc.c).
#include_next <unistd.h>

#include <stdio.h>
#include <sys/types.h>
