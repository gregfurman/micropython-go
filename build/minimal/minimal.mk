# minimal.mk
WASI_SDK ?= /opt/wasi-sdk
CC = $(WASI_SDK)/bin/clang
EMBED_DIR = micropython_embed

SRCS = guest.c types.c $(wildcard $(EMBED_DIR)/*/*.c) $(wildcard $(EMBED_DIR)/*/*/*.c)

# shadowing for sjlj
SRCS += wasm_sjlj.c 

OBJS = $(SRCS:.c=.o)

CFLAGS = -target wasm32-wasip1 -Os \
         -I. -I$(EMBED_DIR) -I$(EMBED_DIR)/port \
         -Wall -fno-common \
         -mllvm -enable-emscripten-sjlj

LDFLAGS = -target wasm32-wasip1 \
          -nostartfiles \
          -Wl,--no-entry \
          -Wl,--export=malloc \
          -Wl,--export=free \
          -Wl,--import-undefined \
          -Wl,--export-table \
          -Wl,--export=__stack_pointer

LDFLAGS += -mexec-model=reactor

all: generate
	$(MAKE) -f minimal.mk guest.wasm

generate:
	$(MAKE) -f micropython_embed.mk

guest.wasm: $(OBJS)
	$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

%.o: %.c
	$(CC) $(CFLAGS) -c $< -o $@

clean:
	rm -rf micropython_embed build-embed guest.wasm $(OBJS)