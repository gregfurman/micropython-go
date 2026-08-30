ROOT ?= ..
MICROPYTHON_TOP ?= ${ROOT}/micropython

SRC_QSTR += main.c types.c

# NOTE: we need to pass in any external modules that we want included in via the caller.
SRC_QSTR += $(EXTMOD_SRC_QSTR)

include $(MICROPYTHON_TOP)/ports/embed/embed.mk

.PHONY: genhdr
genhdr: $(GENHDR_OUTPUT)
