SYNC_SUBMODULE ?= 1

ifeq ($(SYNC_SUBMODULE),1)
ifneq ($(wildcard .git),)
SUBMODULE_SYNC := $(shell \
	git submodule sync --quiet -- micropython 2>/dev/null; \
	git submodule update --init -- micropython 2>/dev/null \
)
endif
endif

ifeq ($(wildcard micropython/py/mkenv.mk),)
$(error MicroPython submodule is missing. Run: git submodule update --init)
endif

V ?= 0
ifeq ($(V),0)
Q := @
endif

BUILD_DIR   := build
OUT_DIR     := out
MPY_DIR     := micropython
EMBED_PORT  := $(MPY_DIR)/ports/embed

# Scratch dir for embed.mk, which is only run to produce $(EMBED_BUILD)/genhdr.
EMBED_BUILD := $(OUT_DIR)/build-embed


WASI_SDK ?= $(firstword $(wildcard $(WASI_SDK_PATH) ./wasi-sdk-*))

ifeq ($(wildcard $(WASI_SDK)/bin/clang),)
$(error No wasi-sdk found. Set WASI_SDK_PATH=/path/to/wasi-sdk, or unpack a release next to this Makefile.)
endif

CC := $(WASI_SDK)/bin/clang

WASM_OPT ?= $(if $(BINARYEN_PATH),\
	$(BINARYEN_PATH)/bin/wasm-opt,\
	$(if $(wildcard /opt/homebrew/opt/binaryen/bin/wasm-opt),\
		/opt/homebrew/opt/binaryen/bin/wasm-opt,\
		wasm-opt))

MINIMAL_WASM := $(OUT_DIR)/guest.wasm
LINKED_WASM  := $(OUT_DIR)/guest.linked.wasm
GO_OUT       := internal/micropython/micropython.go

BUILD_SRCS := \
	$(BUILD_DIR)/guest.c \
	$(BUILD_DIR)/types.c \
	$(BUILD_DIR)/wasm_sjlj.c

EXTMOD_SRCS := \
	$(MPY_DIR)/extmod/modjson.c \
	$(MPY_DIR)/extmod/modre.c \
	$(MPY_DIR)/extmod/modtime.c

EMBED_SRCS := \
	$(wildcard $(MPY_DIR)/py/*.c) \
	$(wildcard $(EMBED_PORT)/port/*.c) \
	$(EXTMOD_SRCS)

SRCS := $(BUILD_SRCS) $(EMBED_SRCS)
OBJS := $(SRCS:%.c=$(OUT_DIR)/%.o)
DEPS := $(OBJS:.o=.d)

CFLAGS := \
	-target wasm32-wasip1 \
	-Os \
	-I$(BUILD_DIR) \
	-I$(EMBED_BUILD) \
	-I$(MPY_DIR) \
	-I$(EMBED_PORT) \
	-I$(EMBED_PORT)/port \
	-Wall \
	-fno-common \
	-mllvm \
	-enable-emscripten-sjlj

LDFLAGS := \
	-target wasm32-wasip1 \
	-nostartfiles \
	-Wl,--no-entry \
	-Wl,--export=malloc \
	-Wl,--export=free \
	-Wl,--import-undefined \
	-Wl,--export-table \
	-Wl,--export=__stack_pointer \
	-Wl,-z,stack-size=196608 \
	-Wl,--initial-memory=393216 \
	-mexec-model=reactor

WASM_OPT_FEATURES := \
	--enable-mutable-globals \
	--enable-multivalue \
	--enable-nontrapping-float-to-int \
	--enable-sign-ext \
	--enable-reference-types \
	--enable-bulk-memory \
	--enable-extended-const

.PHONY: all wasm2go test clean generate-embed sync

all: wasm2go

micropython/py/mkenv.mk:
	git submodule sync --quiet -- micropython
	git submodule update --init --depth 1 -- micropython

sync:
	git submodule sync --quiet -- micropython
	git submodule update --init --depth 1 -- micropython

wasm2go: $(GO_OUT)

# Prevents re-trigger this on every build.
GENHDR_STAMP := $(EMBED_BUILD)/.genhdr.stamp

$(GENHDR_STAMP): $(SRCS) $(wildcard $(BUILD_DIR)/*.h) $(BUILD_DIR)/micropython_embed.mk Makefile
	$(Q)$(MAKE) \
		-C $(BUILD_DIR) \
		-f micropython_embed.mk \
		MICROPYTHON_TOP=../$(MPY_DIR) \
		BUILD=../$(EMBED_BUILD) \
		EXTMOD_SRC_QSTR="$(addprefix ../,$(EXTMOD_SRCS))" \
		genhdr
	$(Q)mkdir -p $(dir $@)
	$(Q)touch $@

generate-embed: $(GENHDR_STAMP)

$(OBJS): | $(GENHDR_STAMP)

$(OUT_DIR)/%.o: %.c Makefile
	$(Q)mkdir -p $(dir $@)
	$(Q)$(CC) $(CFLAGS) -MMD -MP -c $< -o $@

$(LINKED_WASM): $(OBJS) Makefile
	$(Q)mkdir -p $(dir $@)
	$(Q)$(CC) -o $@ $(OBJS) $(LDFLAGS)

$(MINIMAL_WASM): $(LINKED_WASM) Makefile
	$(Q)$(WASM_OPT) \
		--spill-pointers \
		$(WASM_OPT_FEATURES) \
		-o $@ \
		$<

$(GO_OUT): $(MINIMAL_WASM) Makefile
	$(Q)go tool wasm2go -embed -unsafe -o $@ $<
	$(Q)gofmt -w $@

test: wasm2go
	$(Q)go test ./...

clean:
	$(Q)rm -rf $(OUT_DIR)

-include $(DEPS)
