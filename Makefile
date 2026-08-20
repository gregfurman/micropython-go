# The C sources live in build/; this is only the output directory, so it has to
# be named before mkenv.mk defaults it to "build".
BUILD ?= out

# Bring the MicroPython submodule to the pinned revision before any of its
# makefiles are included below.  Best-effort by design: this is silent when
# there is nothing to do, and silent when it cannot run at all -- an
# uncommitted tree has no gitlink in HEAD for `git submodule update` to read,
# and an unpacked tarball has no git.  The guard underneath is what actually
# enforces the submodule being there.
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

# qstr definitions (must come before including py.mk)
QSTR_DEFS = build/qstrdefsport.h

# MicroPython feature configurations
MICROPY_ROM_TEXT_COMPRESSION ?= 1

# Path to an unpacked wasi-sdk release (https://github.com/WebAssembly/wasi-sdk).
# Set WASI_SDK_PATH, or unpack a release next to this Makefile.
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

# include py core make definitions
include $(TOP)/py/py.mk

# py.mk names mpconfigport.h as a bare prerequisite, so make has to be told
# where the port's headers live.
vpath %.h build

# build/ first: setjmp.h in there has to shadow wasi-libc's, which refuses to
# compile without exception handling.
INC += -Ibuild
INC += -I$(TOP)
INC += -I$(BUILD)

# Size of the Wasm shadow stack (the slice of linear memory used for alloca and
# for locals whose address is taken).  The wasm-ld default of 64k is not enough
# for the parser and compiler, which recurse on nested expressions.
WASM_STACK_SIZE ?= 1048576

# How much C stack MicroPython will let itself use before raising
# RuntimeError.  Note this is NOT bounded by WASM_STACK_SIZE in practice: every
# call out of a function containing an nlr_push goes through a host invoke_*
# trampoline (see wasm_sjlj.c), so each Python frame also costs a *host* stack
# frame, and the host runs out first.  With SPILL_POINTERS=1 a Python frame
# costs ~274 bytes of shadow stack, so this allows a recursion depth of about
# 359.  Going much beyond that needs a host with a bigger stack: `node` wants
# --stack-size (see the run/test targets below), while Go's goroutine stacks
# grow on demand and cope with far more.  Re-run `make clean` after changing
# this -- the build does not track CFLAGS.
MICROPY_C_STACK_SIZE ?= 98304

MICROPY_HEAP_SIZE ?= 2097152

CFLAGS += $(INC) -Wall -Werror -Wdouble-promotion -Wfloat-conversion -std=gnu99 $(COPT)
CFLAGS += -DMICROPY_HEAP_SIZE=$(MICROPY_HEAP_SIZE)
CFLAGS += -DMICROPY_C_STACK_SIZE=$(MICROPY_C_STACK_SIZE)

# MicroPython's NLR needs setjmp/longjmp, which Wasm cannot do on its own.
# Rather than wasi-libc's implementation (-mllvm -wasm-enable-sjlj -lsetjmp),
# which requires the Wasm exception handling proposal, use LLVM's
# Emscripten-style lowering: no exception handling ends up in the module, and
# the unwind is delegated to the host through imported invoke_* trampolines.
# See wasm_sjlj.c.
CFLAGS += -mllvm -enable-emscripten-sjlj

# Pin the Wasm feature set to what wasm2go accepts (the same list
# ncruces/go-sqlite3-wasm builds with), minus tail calls, whose behaviour
# wasm2go does not guarantee.  Notably this must not include exception
# handling.
WASM_FEATURES ?= \
	-mmutable-globals \
	-mmultivalue \
	-mnontrapping-fptoint \
	-msign-ext \
	-mreference-types \
	-mbulk-memory \
	-mextended-const

CFLAGS += $(WASM_FEATURES)

# Reactor, not command: there is no main(), the host calls _initialize and then
# drives the module through the exports in wasm_api.c.  Link-time only.
LDFLAGS += -mexec-model=reactor

LDFLAGS += -Wl,--gc-sections
# The invoke_* trampolines and _emscripten_throw_longjmp come from the host,
# and reach back into the module through the indirect function table.
LDFLAGS += -Wl,--import-undefined -Wl,--export-table
# The host has to rewind the shadow stack after an unwind.
LDFLAGS += -Wl,--export=__stack_pointer
# Put the shadow stack at the bottom of linear memory so that overflowing it
# traps instead of silently corrupting the heap.
LDFLAGS += -Wl,--stack-first -Wl,-z,stack-size=$(WASM_STACK_SIZE)

CSUPEROPT = -Os # save some code space

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

SRC_QSTR += build/wasm_api.c build/wasm_build.c

OBJ += $(PY_CORE_O)
OBJ += $(addprefix $(BUILD)/, $(SRC_C:.c=.o))

# Binaryen's --spill-pointers forces pointer-typed Wasm locals into the shadow
# stack, so that gc_collect()'s conservative scan can see them.  Without it the
# GC will eventually collect a live object that is only referenced from a Wasm
# local, which shows up as a null indirect call much later.  Not optional in
# practice; SPILL_POINTERS=0 exists only for measuring the difference.
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

$(BUILD)/micropython.linked.wasm: $(OBJ)
	$(ECHO) "LINK $@"
	$(Q)$(LD) $(LDFLAGS) -o $@ $^ $(LIBS)

ifeq ($(SPILL_POINTERS), 1)
$(BUILD)/micropython.wasm: $(BUILD)/micropython.linked.wasm
	$(ECHO) "SPILL $@"
	$(Q)$(WASM_OPT) --spill-pointers $(WASM_OPT_FEATURES) -o $@ $<
	$(Q)ls -l $@
else
$(BUILD)/micropython.wasm: $(BUILD)/micropython.linked.wasm
	$(Q)cp $< $@
	$(Q)ls -l $@
endif

# Translate the module to Go with wasm2go.  internal/env supplies the invoke_*
# trampolines and internal/wasip1 the WASI syscalls; see cmd/micropython.
GO_OUT = internal/micropython/micropython.go

# NB: not named "go" -- with go.mod present that collides with make's built-in
# Modula-2 rule (%: %.mod).
.PHONY: wasm2go test
wasm2go: $(GO_OUT)

$(GO_OUT): $(BUILD)/micropython.wasm
	$(ECHO) "WASM2GO $@"
	$(Q)go tool wasm2go -embed -unsafe -o $@ $<
	$(Q)gofmt -w $@

test: $(GO_OUT)
	$(Q)go test ./...

include $(TOP)/py/mkrules.mk
