MICROPYTHON_TOP ?= ../micropython

# guest.c and types.c are compiled by the top-level Makefile, not as part of
# this build, so the extractor never sees them on its own. List them here so
# their MP_QSTR_* and MP_REGISTER_ROOT_POINTER() uses make it into genhdr.
# Must be set before embed.mk includes py.mk.
SRC_QSTR += guest.c types.c

# EXTMOD_SRC_QSTR comes from the top-level Makefile's EXTMOD_SRCS so the two
# lists cannot drift. A module missing here compiles but never registers.
SRC_QSTR += $(EXTMOD_SRC_QSTR)

include $(MICROPYTHON_TOP)/ports/embed/embed.mk

# The top-level build compiles MicroPython in place from the submodule, so the
# generated headers are the only thing it needs from here. This skips embed.mk's
# packaging step, which exists to produce a standalone tree for projects that do
# not vendor MicroPython.
.PHONY: genhdr
genhdr: $(GENHDR_OUTPUT)
