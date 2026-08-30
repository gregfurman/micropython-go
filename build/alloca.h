#pragma once

// port/mpconfigport_common.h includes <alloca.h> unconditionally on non-BSD,
// non-Windows targets. libc-gen ships no such header and puts the macro in
// stdlib.h, which is the branch that header already takes for BSD.
#include <stdlib.h>
