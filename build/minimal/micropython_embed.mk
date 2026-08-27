MICROPYTHON_TOP ?= ../../micropython

# guest.c and types.c are compiled by minimal.mk, not as part of the embed
# package, so the extractor never sees them on its own. List them here so their
# MP_QSTR_* and MP_REGISTER_ROOT_POINTER() uses make it into genhdr.
# Must be set before embed.mk includes py.mk.
SRC_QSTR += guest.c types.c

include $(MICROPYTHON_TOP)/ports/embed/embed.mk