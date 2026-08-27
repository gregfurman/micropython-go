/*
 * Wasm has no way to unwind its own call stack, so wasi-libc's <setjmp.h>
 * refuses to compile unless the Wasm exception handling proposal is enabled.
 * This header shadows it (the port directory comes first on the include path)
 * for the Emscripten-style SjLj lowering instead, which needs no exception
 * handling in the module at all -- see wasm_sjlj.c and README.md.
 *
 * LLVM rewrites every setjmp/longjmp call itself (-mllvm
 * -enable-emscripten-sjlj), so these declarations only have to exist; they are
 * never actually called.
 */

#pragma once

// Must be large enough, and aligned enough, for struct jmp_buf_impl in
// wasm_sjlj.c.
typedef unsigned long jmp_buf[8];

__attribute__((returns_twice)) int setjmp(jmp_buf);
__attribute__((noreturn)) void longjmp(jmp_buf, int);
