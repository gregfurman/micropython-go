ROOT ?= ..
MICROPYTHON_TOP ?= ${ROOT}/micropython

# Every source of ours that the qstr and root-pointer scanners have to see.
SRC_QSTR += arena.c decode.c encode.c exec.c gccollect.c hostfn.c main.c mphalport.c pymodule.c refs.c vm.c

# NOTE: we need to pass in any external modules that we want included in via the caller.
SRC_QSTR += $(EXTMOD_SRC_QSTR)

include $(MICROPYTHON_TOP)/ports/embed/embed.mk

.PHONY: genhdr
genhdr: $(GENHDR_OUTPUT)
