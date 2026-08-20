// Package exec embeds MicroPython.
//
// # Crossing the boundary
//
// wasm2go can only carry scalars across the module boundary, so every export
// in wasm_api.c takes and returns i32, with anything larger passed as an
// offset into linear memory. That part is handled here by writeString and
// readString.
//
// Values are a separate problem. A MicroPython value is an mp_obj_t: a tagged
// pointer into a garbage-collected heap. Go cannot hold one, because it has no
// way to keep it rooted across a collection, so something else has to cross.
//
// On the way out, wasm_value.c walks the result and streams it through the
// val_* callbacks this package imports, and value.go rebuilds it as native Go
// values. That beats serialising through JSON on both counts that matter here:
// there is no encode and no parse, and the types Go cares about survive, where
// JSON would collapse int and float into one numeric type, lose bytes, and
// make tuples indistinguishable from lists.
//
// On the way in, push.go is the mirror image: Go pushes scalars and asks for
// the container that collects them, and the module assembles the value on a
// stack the GC traces. Nothing is rendered as Python source, so there is no
// quoting to get wrong and no parse to pay for.
package exec

import (
	"errors"
	"fmt"

	"github.com/gregfurman/micropython-wasi/internal/env"
	wasi "github.com/gregfurman/micropython-wasi/internal/micropython"
)

// mp_api_eval modes; must match wasm_api.h.
const (
	modeExec  = 0 // run as a script
	modeValue = 1 // evaluate one expression, streamed to the val_* callbacks
)

const apiOK = 0

var errInvalidType = errors.New("invalid type evaluated")

// Error is a Python exception that escaped to the host. Text is the traceback
// as MicroPython formatted it.
type Error struct {
	Text string
}

func (e *Error) Error() string {
	return e.Text
}

// Instance is one MicroPython interpreter. It owns its own linear memory and
// heap, so separate Instances are independent, but a single Instance is not
// safe for concurrent use.
type Instance struct {
	mod    *wasi.Module
	values *builder
	packer packer
}

// New boots an interpreter. The module imports nothing but the two host
// interfaces in internal/env and this package, so there is no filesystem, no
// stdio and no WASI to configure.
func New() (*Instance, error) {
	values := &builder{}
	mod := wasi.New(env.New(), values)

	in := &Instance{mod: mod, values: values}
	// _initialize runs the module's constructors, one of which boots the
	// interpreter, so there is nothing else to set up.
	mod.X_initialize()
	return in, nil
}

// Exec runs src as a script and returns whatever it printed.
func (i *Instance) Exec(src string) (string, error) {
	if err := i.eval(src, modeExec); err != nil {
		return "", err
	}
	return i.out(), nil
}

// Eval evaluates a single expression and returns the result as a native Go
// value, built directly by the host callbacks in value.go:
//
//	None            nil
//	bool            bool
//	int             int64
//	float           float64
//	str             string
//	bytes           []byte
//	list            []any
//	tuple           Tuple
//	dict            map[string]any, or map[any]any for non-string keys
//	anything else   Object{Type, Repr}
func (i *Instance) Eval(expr string) (any, error) {
	i.values.reset()
	if err := i.eval(expr, modeValue); err != nil {
		return nil, err
	}
	return i.values.result()
}

// TypedEval is Eval with the result coerced to T. A method cannot take type
// parameters, so this is a function.
func TypedEval[T any](i *Instance, expr string) (T, error) {
	var zero T
	res, err := i.Eval(expr)
	if err != nil {
		return zero, err
	}

	return Coerce[T](res)
}

// Set binds a Go value to a Python global. This is how you pass arguments in:
//
//	in.Set("rows", []int{1, 2, 3})
//	total, err := in.Eval("sum(rows)")
func (i *Instance) Set(name string, value any) error {
	// Name first, then the encoded value, in one scratch buffer so this is a
	// single crossing.
	i.packer.reset()
	i.packer.buf = append(i.packer.buf, name...)
	if err := i.packer.value(value, 0); err != nil {
		return err
	}

	ptr, err := i.writeScratchBytes(i.packer.buf)
	if err != nil {
		return err
	}
	return i.check(i.mod.Xmp_api_set_global(ptr, int32(len(name)),
		ptr+int32(len(name)), int32(len(i.packer.buf)-len(name))))
}

// Call invokes a Python global by name. For a function called more than once,
// resolve it with Func or Define instead and call that: the name lookup then
// happens once rather than per call.
func (i *Instance) Call(name string, args ...any) (any, error) {
	fn, err := i.Func(name)
	if err != nil {
		return nil, err
	}
	return fn.Call(args...)
}

// Output returns what the last call printed.
func (i *Instance) Output() string { return i.out() }

// -----------------------------------------------------------------

func (i *Instance) eval(src string, mode int32) error {
	ptr, err := i.writeScratch(src)
	if err != nil {
		return err
	}

	if rc := i.mod.Xmp_api_eval(ptr, int32(len(src)), mode); rc != apiOK {
		text := i.readString(i.mod.Xmp_api_err(), i.mod.Xmp_api_err_len())
		if text == "" {
			text = "unknown error"
		}
		return &Error{Text: text}
	}
	return nil
}

// check turns a non-OK return into the Python traceback the module recorded.
func (i *Instance) check(rc int32) error {
	if rc == apiOK {
		return nil
	}
	text := i.readString(i.mod.Xmp_api_err(), i.mod.Xmp_api_err_len())
	if text == "" {
		text = "unknown error"
	}
	return &Error{Text: text}
}

func (i *Instance) out() string {
	return i.readString(i.mod.Xmp_api_out(), i.mod.Xmp_api_out_len())
}

// writeScratch copies s into the module's reusable scratch buffer. The result
// is only valid until the next call that uses scratch, which is fine because
// every consumer copies out of it immediately.
func (i *Instance) writeScratch(s string) (int32, error) {
	ptr := i.mod.Xmp_api_scratch(int32(len(s)))
	if ptr == 0 {
		return 0, fmt.Errorf("micropython: out of memory reserving %d bytes", len(s))
	}
	copy((*i.mod.Xmemory().Slice())[ptr:], s)
	return ptr, nil
}

// writeScratchBytes is writeScratch for an already-encoded buffer.
func (i *Instance) writeScratchBytes(b []byte) (int32, error) {
	ptr := i.mod.Xmp_api_scratch(int32(len(b)))
	if ptr == 0 && len(b) > 0 {
		return 0, fmt.Errorf("micropython: out of memory reserving %d bytes", len(b))
	}
	copy((*i.mod.Xmemory().Slice())[ptr:], b)
	return ptr, nil
}

func (i *Instance) readString(ptr, length int32) string {
	if ptr == 0 || length <= 0 {
		return ""
	}
	mem := *i.mod.Xmemory().Slice()
	return string(mem[ptr : ptr+length])
}
