#!/usr/bin/env bash
set -euo pipefail

cd -P -- "$(dirname -- "$0")"

ROOT=..
WASI_SDK="${WASI_SDK:-$ROOT/tools/wasi-sdk}/bin"
BINARYEN="${BINARYEN:-$ROOT/tools/binaryen}/bin"

EXTMOD_SRCS="$ROOT/micropython/extmod/modjson.c \
	$ROOT/micropython/extmod/modre.c \
	$ROOT/micropython/extmod/modtime.c"


go tool libc-gen -c-out "$ROOT/libc"

trap 'rm -f micropython micropython.wasm' EXIT

make V=1 -f micropython_embed.mk \
	ROOT="$ROOT" \
	BUILD=build-embed \
	EXTMOD_SRC_QSTR="$EXTMOD_SRCS" \
	genhdr

GO_OUT="$ROOT/internal/micropython/micropython.go"

mkdir -p "$(dirname "$GO_OUT")"

# formatfloat.c and parsenum.c use long double, which is 128-bit here and needs
# compiler-rt soft-float that -nostdlib would otherwise drop.
BUILTINS="$("$WASI_SDK/clang" -print-resource-dir)/lib/wasm32-unknown-wasi/libclang_rt.builtins.a"

"$WASI_SDK/clang" --target=wasm32 -ffreestanding -nostdlib -Os -Wall -fno-common \
	-o micropython \
	main.c types.c wasm_sjlj.c \
	$ROOT/libc/libc.c \
	$ROOT/libc/malloc_sbrk.c \
	$ROOT/micropython/py/*.c \
	$ROOT/micropython/ports/embed/port/*.c \
	$EXTMOD_SRCS \
	-I. \
	-I$ROOT/libc \
	-Ibuild-embed \
	-I$ROOT/micropython \
	-I$ROOT/micropython/ports/embed \
	-I$ROOT/micropython/ports/embed/port \
	-mllvm -enable-emscripten-sjlj \
	-mexec-model=reactor \
	-Wl,--no-entry \
	-Wl,--import-undefined \
	-Wl,--export-table \
	-Wl,--export=malloc \
	-Wl,--export=free \
 	-Wl,--import-memory \
	-Wl,--export=__stack_pointer \
	-Wl,-z,stack-size=196608 \
	-Wl,--initial-memory=393216 \
	"$BUILTINS"

"$BINARYEN/wasm-opt" -g micropython -o micropython.wasm \
	--spill-pointers \
	--enable-mutable-globals --enable-multivalue \
	--enable-nontrapping-float-to-int --enable-sign-ext \
	--enable-reference-types --enable-bulk-memory \
	--enable-extended-const

go tool libc-gen -wasm micropython.wasm \
    -o $ROOT/internal/micropython/libc.go \
    -pkg micropython

go tool wasm2go -embed -unsafe \
    -provided $ROOT/internal/micropython/invoke.go \
    -provided $ROOT/internal/micropython/clock.go \
    -provided $ROOT/internal/micropython/libc.go \
    -o "$GO_OUT" micropython.wasm

gofmt -w "$GO_OUT"

