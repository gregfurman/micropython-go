package impl

import (
	wasi "github.com/gregfurman/micropython-go/internal/minimal"
)

type Host interface {
	Invoke(funcID, argsPtr, numArgs, outPtr int32)
	Stdout(ptr, len int32)
}

type Env struct {
	mod  *wasi.Module
	host Host
}

func NewEnv(h Host) *Env {
	return &Env{host: h}
}

func (e *Env) Xhost_trampoline(funcID, argsPtr, numArgs, outPtr int32) {
	e.host.Invoke(funcID, argsPtr, numArgs, outPtr)
}

func (e *Env) Xhost_stdout(ptr, n int32) {
	e.host.Stdout(ptr, n)
}

// longjmp is the panic value used for a module-initiated unwind. Anything else
// panicking through an invoke_* is a genuine failure and is re-panicked.
type longjmp struct{}

var _ wasi.Xenv = (*Env)(nil)

// // Init is called by micropython.New with the module being constructed.
func (e *Env) Init(m any) {
	e.mod = m.(*wasi.Module)
}

// X_emscripten_throw_longjmp is called by the module's emscripten_longjmp,
// after it has recorded the target jmp_buf in __THREW__/__threwValue. It must
// not return.
func (e *Env) X_emscripten_throw_longjmp() {
	panic(longjmp{})
}

func (e *Env) invoke[F any, V comparable](idx int32, call func(F) V) (ret V) {
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

func (e *Env) Xinvoke_di(v0, v1 int32) float64 {
	return e.invoke(v0, func(f func(int32) float64) float64 { return f(v1) })
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
