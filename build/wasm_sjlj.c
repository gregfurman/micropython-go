#include <stdint.h>

// jmp_buf identity.  func_invocation_id is a pointer to an alloca in the frame
// that called setjmp, so it is unique per invocation and dies with the frame.

struct jmp_buf_impl {
    void* func_invocation_id;
    uint32_t label;
};

void __wasm_setjmp(void* env, uint32_t label, void* func_invocation_id) {
    struct jmp_buf_impl* jb = env;
    jb->func_invocation_id = func_invocation_id;
    jb->label = label;
}

// Returns the label to resume at, or 0 if env belongs to an outer frame and the
// unwind has to continue.
uint32_t __wasm_setjmp_test(void* env, void* func_invocation_id) {
    struct jmp_buf_impl* jb = env;
    return jb->func_invocation_id == func_invocation_id ? jb->label : 0;
}

// Set by the host after an invoke_* unwound, polled by LLVM's generated code.

uintptr_t __THREW__ = 0;
int __threwValue = 0;

__attribute__((export_name("setThrew"))) void setThrew(uintptr_t threw, int value) {
    if (__THREW__ == 0) {
        __THREW__ = threw;
        __threwValue = value;
    }
}

static int temp_ret0;

int getTempRet0(void) { return temp_ret0; }

void setTempRet0(int value) { temp_ret0 = value; }

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
