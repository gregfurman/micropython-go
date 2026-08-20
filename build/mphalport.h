#include "py/mpconfig.h"

// Nothing in this build has a clock, a terminal, or a way to be interrupted.
static inline void mp_hal_set_interrupt_char(char c) {
    (void)c;
}
