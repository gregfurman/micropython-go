/*
 * Runtime support for the Emscripten-style setjmp/longjmp lowering
 * (-mllvm -enable-emscripten-sjlj), which MicroPython's NLR needs.
 *
 * Wasm cannot unwind its own call stack, and the Wasm exception handling
 * proposal is not available to us, so the unwind is delegated to the host:
 *
 *   - LLVM rewrites every call made from a function that contains a setjmp
 *     into a call to an imported `invoke_<sig>` trampoline;
 *   - the host calls the real target through the indirect function table,
 *     inside its own try/catch (a `recover()` in Go);
 *   - longjmp() reaches _emscripten_throw_longjmp(), an import, which makes
 *     the host unwind its frames;
 *   - the host catches that, restores __stack_pointer to where the invoke
 *     started, and calls the exported setThrew() to flag what happened;
 *   - back in the module, LLVM's generated code sees __THREW__ set and
 *     dispatches to the matching setjmp label, or rethrows to the next
 *     invoke_* out if the jmp_buf belongs to an outer frame.
 *
 * That last step is what makes nesting work, and is the difference between
 * this and a "setjmp always returns 0" stub: MicroPython pushes and pops NLR
 * buffers constantly, and every Python try/except depends on landing at the
 * innermost handler rather than the outermost one.
 *
 * The host side is internal/env in Go.
 */

#include <stdint.h>

// jmp_buf identity.  func_invocation_id is a pointer to an alloca in the frame
// that called setjmp, so it is unique per invocation and dies with the frame.

struct jmp_buf_impl {
    void *func_invocation_id;
    uint32_t label;
};

void __wasm_setjmp(void *env, uint32_t label, void *func_invocation_id) {
    struct jmp_buf_impl *jb = env;
    jb->func_invocation_id = func_invocation_id;
    jb->label = label;
}

uint32_t __wasm_setjmp_test(void *env, void *func_invocation_id) {
    struct jmp_buf_impl *jb = env;
    if (jb->func_invocation_id == func_invocation_id) {
        return jb->label;
    }
    return 0;
}

// Set by the host after an invoke_* unwound, polled by LLVM's generated code.

uintptr_t __THREW__ = 0;
int __threwValue = 0;

__attribute__((export_name("setThrew")))
void setThrew(uintptr_t threw, int value) {
    if (__THREW__ == 0) {
        __THREW__ = threw;
        __threwValue = value;
    }
}

static int temp_ret0;

int getTempRet0(void) {
    return temp_ret0;
}

void setTempRet0(int value) {
    temp_ret0 = value;
}

// Provided by the host: unwinds the host's frames.  Never returns.
extern void _emscripten_throw_longjmp(void);

void emscripten_longjmp(uintptr_t env, int val) {
    // C standard: longjmp cannot make setjmp return 0.
    if (val == 0) {
        val = 1;
    }
    setThrew(env, val);
    _emscripten_throw_longjmp();
}
