// NOTE: This is needed for netutils.c (the only caller) for %u.

#include <stdarg.h>
#include <stddef.h>
#include <string.h>

#include "py/mpprint.h"

// len counts what the format produced, not what fit, because that is what
// snprintf returns. cap is the room for content, one short of the buffer.
typedef struct {
    char* buf;
    size_t cap;
    size_t len;
} bounded_print_t;

static void bounded_print_strn(void* data, const char* str, size_t len) {
    bounded_print_t* out = data;

    if (out->len < out->cap) {
        size_t room = out->cap - out->len;
        memcpy(out->buf + out->len, str, len < room ? len : room);
    }

    out->len += len;
}

int vsnprintf(char* buf, size_t size, const char* fmt, va_list args) {
    bounded_print_t out = {.buf = buf, .cap = size == 0 ? 0 : size - 1, .len = 0};
    mp_print_t print = {&out, bounded_print_strn};

    mp_vprintf(&print, fmt, args);

    if (size != 0) {
        buf[out.len < out.cap ? out.len : out.cap] = '\0';
    }

    return (int)out.len;
}
