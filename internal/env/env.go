// Package env is the host half of MicroPython's setjmp/longjmp.
//
// Wasm cannot unwind its own call stack, and this module is deliberately built
// without the exception handling proposal (see ../../README.md), so LLVM's
// Emscripten-style SjLj lowering routes every call made from a function
// containing an nlr_push through one of the invoke_* trampolines below. The
// unwind itself happens here, in Go:
//
//   - longjmp() reaches X_emscripten_throw_longjmp, which panics;
//   - the panic unwinds the generated Go frames, standing in for the C frames
//     the module cannot unwind itself;
//   - the invoke_* that made the call recovers, rewinds __stack_pointer to
//     where the call started, and calls the module's setThrew();
//   - back in the module, the generated code sees __THREW__ set and either
//     dispatches to the matching setjmp label or falls through to the next
//     invoke_* out, which is what makes nested handlers work.
//
// Every Python try/except depends on that last step landing at the innermost
// matching handler, so a "setjmp always returns 0, longjmp panics to the top"
// shortcut is not sufficient here.
package env

import (
	wasi "github.com/gregfurman/micropython-wasi/internal/micropython"
)

// longjmp is the panic value used for a module-initiated unwind. Anything else
// panicking through an invoke_* is a genuine failure and is re-panicked.
type longjmp struct{}

type Env struct {
	mod *wasi.Module
}

var _ wasi.Xenv = (*Env)(nil)

// New returns an Env ready to be passed to micropython.New. The module back
// reference is filled in by Init, which micropython.New calls.
func New() *Env {
	return &Env{}
}

// Init is called by micropython.New with the module being constructed.
func (e *Env) Init(m any) {
	e.mod = m.(*wasi.Module)
}

// X_emscripten_throw_longjmp is called by the module's emscripten_longjmp,
// after it has recorded the target jmp_buf in __THREW__/__threwValue. It must
// not return.
func (e *Env) X_emscripten_throw_longjmp() {
	panic(longjmp{})
}

func (e *Env) invoke[F any](idx int32, call func(F) int32) (ret int32) {
	// The abandoned frames' shadow stack has to be reclaimed by hand; the
	// generated code only restores __stack_pointer on the normal return path.
	// X__stack_pointer returns a pointer to the live field, so this must
	// dereference now: keeping the pointer would alias the field and make the
	// restore below a no-op.
	sp := *e.mod.X__stack_pointer()

	defer func() {
		r := recover()
		if r == nil {
			return
		}
		if _, ok := r.(longjmp); !ok {
			panic(r)
		}
		*e.mod.X__stack_pointer() = sp
		e.mod.XsetThrew(1, 0)
	}()

	return call((*e.mod.X__indirect_function_table())[idx].(F))
}

// The trampolines. The name after invoke_ is the callee's signature, return
// type first, so invoke_vii calls a func(int32, int32) and invoke_iii calls a
// func(int32, int32) int32. In both cases the leading v0 is the table index,
// not an argument.

func (e *Env) Xinvoke_v(v0 int32) {
	e.invoke(v0, func(f func()) int32 { f(); return 0 })
}

func (e *Env) Xinvoke_vi(v0, v1 int32) {
	e.invoke(v0, func(f func(int32)) int32 { f(v1); return 0 })
}

func (e *Env) Xinvoke_vii(v0, v1, v2 int32) {
	e.invoke(v0, func(f func(int32, int32)) int32 { f(v1, v2); return 0 })
}

func (e *Env) Xinvoke_viii(v0, v1, v2, v3 int32) {
	e.invoke(v0, func(f func(int32, int32, int32)) int32 { f(v1, v2, v3); return 0 })
}

func (e *Env) Xinvoke_viiii(v0, v1, v2, v3, v4 int32) {
	e.invoke(v0, func(f func(int32, int32, int32, int32)) int32 { f(v1, v2, v3, v4); return 0 })
}

func (e *Env) Xinvoke_i(v0 int32) int32 {
	return e.invoke(v0, func(f func() int32) int32 { return f() })
}

// Scalar arguments other than i32 get their own signatures; the linker asks
// for exactly the set the module uses, so this list grows when the API does.
func (e *Env) Xinvoke_ij(v0 int32, v1 int64) int32 {
	return e.invoke(v0, func(f func(int64) int32) int32 { return f(v1) })
}

func (e *Env) Xinvoke_id(v0 int32, v1 float64) int32 {
	return e.invoke(v0, func(f func(float64) int32) int32 { return f(v1) })
}

func (e *Env) Xinvoke_ii(v0, v1 int32) int32 {
	return e.invoke(v0, func(f func(int32) int32) int32 { return f(v1) })
}

func (e *Env) Xinvoke_iii(v0, v1, v2 int32) int32 {
	return e.invoke(v0, func(f func(int32, int32) int32) int32 { return f(v1, v2) })
}

func (e *Env) Xinvoke_iiii(v0, v1, v2, v3 int32) int32 {
	return e.invoke(v0, func(f func(int32, int32, int32) int32) int32 { return f(v1, v2, v3) })
}

func (e *Env) Xinvoke_iiiii(v0, v1, v2, v3, v4 int32) int32 {
	return e.invoke(v0, func(f func(int32, int32, int32, int32) int32) int32 { return f(v1, v2, v3, v4) })
}
