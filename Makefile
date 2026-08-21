BUILD ?= out


SYNC_SUBMODULE ?= 1
ifeq ($(SYNC_SUBMODULE),1)
ifneq ($(wildcard .git),)
SUBMODULE_SYNC := $(shell \
	git submodule sync --quiet -- micropython 2>/dev/null; \
	git submodule update --init -- micropython 2>/dev/null)
ifneq ($(strip $(SUBMODULE_SYNC)),)
$(info $(SUBMODULE_SYNC))
endif
endif
endif

ifeq ($(wildcard micropython/py/mkenv.mk),)
$(error MicroPython submodule is missing. Run: git submodule update --init)
endif

include micropython/py/mkenv.mk

# Must come before py.mk.
QSTR_DEFS = build/qstrdefsport.h

MICROPY_ROM_TEXT_COMPRESSION ?= 1

# https://github.com/WebAssembly/wasi-sdk
WASI_SDK ?= $(firstword $(wildcard $(WASI_SDK_PATH) ./wasi-sdk-*))
WASI_TARGET ?= wasm32-wasip1

ifeq ($(wildcard $(WASI_SDK)/bin/clang),)
$(error No wasi-sdk found. Set WASI_SDK_PATH=/path/to/wasi-sdk, or unpack a \
release next to this Makefile. wasi-sdk 25 or newer is required.)
endif

CC = $(WASI_SDK)/bin/clang --target=$(WASI_TARGET)
LD = $(CC)
AR = $(WASI_SDK)/bin/llvm-ar
SIZE = $(WASI_SDK)/bin/llvm-size
STRIP = $(WASI_SDK)/bin/llvm-strip

include $(TOP)/py/py.mk

# py.mk names mpconfigport.h as a bare prerequisite.
vpath %.h build

# build/ first: its setjmp.h has to shadow wasi-libc's, which refuses to
# compile without exception handling.
INC += -Ibuild
INC += -I$(TOP)
INC += -I$(BUILD)

# wasm-ld's 64k default is not enough for the parser and compiler, which
# recurse on nested expressions.
WASM_STACK_SIZE ?= 1048576

# Not bounded by WASM_STACK_SIZE: every Python frame also costs a host stack
# frame in an invoke_* trampoline, and the host runs out first. ~274 bytes per
# frame with SPILL_POINTERS=1, so this allows a recursion depth of about 359.
# Needs `make clean` to take effect -- the build does not track CFLAGS.
MICROPY_C_STACK_SIZE ?= 98304

MICROPY_HEAP_SIZE ?= 2097152

CFLAGS += $(INC) -Wall -Werror -Wdouble-promotion -Wfloat-conversion -std=gnu99 $(COPT)
CFLAGS += -DMICROPY_HEAP_SIZE=$(MICROPY_HEAP_SIZE)
CFLAGS += -DMICROPY_C_STACK_SIZE=$(MICROPY_C_STACK_SIZE)

# NLR needs setjmp/longjmp. wasi-libc's (-mllvm -wasm-enable-sjlj -lsetjmp)
# requires the Wasm exception handling proposal; this lowering puts no
# exception handling in the module and delegates the unwind to the host. See
# build/wasm_sjlj.c.
CFLAGS += -mllvm -enable-emscripten-sjlj

# What wasm2go accepts, minus tail calls, whose behaviour it does not
# guarantee. Must NOT include exception handling as its not handled.
WASM_FEATURES ?= \
	-mmutable-globals \
	-mmultivalue \
	-mnontrapping-fptoint \
	-msign-ext \
	-mreference-types \
	-mbulk-memory \
	-mextended-const

CFLAGS += $(WASM_FEATURES)

# Link-time only; clang rejects it as a compile flag.
LDFLAGS += -mexec-model=reactor

LDFLAGS += -Wl,--gc-sections
# invoke_* and _emscripten_throw_longjmp come from the host and reach back in
# through the indirect function table.
LDFLAGS += -Wl,--import-undefined -Wl,--export-table
# The host rewinds the shadow stack after an unwind.
LDFLAGS += -Wl,--export=__stack_pointer
# Shadow stack at the bottom of memory, so overflowing it traps rather than
# silently corrupting the heap.
LDFLAGS += -Wl,--stack-first -Wl,-z,stack-size=$(WASM_STACK_SIZE)

CSUPEROPT = -Os

ifeq ($(DEBUG), 1)
CFLAGS += -O0 -g
else
CFLAGS += -Os -DNDEBUG
CFLAGS += -fdata-sections -ffunction-sections
endif

LIBS =

SRC_C = \
	build/main.c \
	build/wasm_api.c \
	build/wasm_value.c \
	build/wasm_build.c \
	build/wasm_sjlj.c \

# Just include json and re packages.
SRC_C += \
	micropython/extmod/modjson.c \
	micropython/extmod/modre.c \

SRC_QSTR += build/wasm_api.c build/wasm_build.c
SRC_QSTR += micropython/extmod/modjson.c micropython/extmod/modre.c

OBJ += $(PY_CORE_O)
OBJ += $(addprefix $(BUILD)/, $(SRC_C:.c=.o))

# Forces pointer-typed Wasm locals into the shadow stack, where gc_collect()'s
# conservative scan can see them. Without it the GC eventually frees a live
# object held only in a Wasm local, surfacing much later as a null indirect
# call. SPILL_POINTERS=0 exists only to measure the difference.
SPILL_POINTERS ?= 1
WASM_OPT ?= $(if $(BINARYEN_PATH),$(BINARYEN_PATH)/bin/wasm-opt,wasm-opt)
WASM_OPT_FEATURES = \
	--enable-mutable-globals \
	--enable-multivalue \
	--enable-nontrapping-float-to-int \
	--enable-sign-ext \
	--enable-reference-types \
	--enable-bulk-memory \
	--enable-extended-const

all: $(BUILD)/micropython.wasm

# minifies... 	
$(BUILD)/micropython.linked.wasm: $(OBJ)
	$(Q)$(LD) $(LDFLAGS) -o $@ $^ $(LIBS)

ifeq ($(SPILL_POINTERS), 1)
$(BUILD)/micropython.wasm: $(BUILD)/micropython.linked.wasm
	$(Q)$(WASM_OPT) --spill-pointers $(WASM_OPT_FEATURES) -o $@ $<
	$(Q)ls -l $@
else
$(BUILD)/micropython.wasm: $(BUILD)/micropython.linked.wasm
	$(Q)cp $< $@
	$(Q)ls -l $@
endif

GO_OUT = internal/micropython/micropython.go

.PHONY: wasm2go test
wasm2go: $(GO_OUT)

$(GO_OUT): $(BUILD)/micropython.wasm
	$(Q)go tool wasm2go -embed -pkg micropython -unsafe -o $@ $<
	$(Q)gofmt -w $@

test: $(GO_OUT)
	$(Q)go test ./...

include $(TOP)/py/mkrules.mk
