package host

import (
	"fmt"

	"github.com/gregfurman/micropython-go/internal/env"
	wasi "github.com/gregfurman/micropython-go/internal/micropython"
	"github.com/gregfurman/micropython-go/internal/value"
)

// The module's exports, typed.

// Must match build/wasm_api.h.
const (
	ModeExec  = 0 // run as a script, the result is whatever it printed
	ModeValue = 1 // evaluate one expression, streamed back through val_*
)

const apiOK = 0

// New boots an interpreter.
func New() (*ABI, error) {
	a := newABI()
	if err := a.init(); err != nil {
		return nil, err
	}
	return a, nil
}

// newABI instantiates the module without booting the interpreter, which is
// what a restore wants: the memory it is about to lay down was taken after the
// boot already happened.
//
// The stack base is read here, before anything has run, because that is the
// one moment __stack_pointer is guaranteed to hold the value the linker gave
// it. Reading it beats repeating WASM_STACK_SIZE on this side, where it would
// drift.
func newABI() *ABI {
	a := &ABI{}
	a.mod = wasi.New(env.New(), a)
	a.base = *a.mod.X__stack_pointer()
	return a
}

// Set binds a value to a global name, so a host can seed configuration into an
// interpreter without going through source text.
func (a *ABI) Set(name string, v value.Value) (err error) {
	if err := a.status(); err != nil {
		return err
	}
	defer a.guard(&err)

	lowered, err := lower(v)
	if err != nil {
		return err
	}

	// One buffer, name and value together: the scratch moves when it grows, so
	// a pointer taken before the second write would dangle after it.
	buf := make([]byte, 0, len(name)+len(lowered))
	buf = append(append(buf, name...), lowered...)

	ptr, err := a.Write(buf)
	if err != nil {
		return err
	}

	n := int32(len(name))
	return a.check(a.mod.Xmp_api_set(ptr, n, ptr+n, int32(len(lowered))))
}

func (a *ABI) init() (err error) {
	defer a.guard(&err)
	a.mod.X_initialize()
	return nil
}

// Eval runs src. In ModeValue the result is available from Value.
func (a *ABI) Eval(src string, mode int32) (err error) {
	if err := a.status(); err != nil {
		return err
	}
	defer a.guard(&err)

	a.dec.reset()
	ptr, err := a.WriteString(src)
	if err != nil {
		return err
	}
	return a.check(a.mod.Xmp_api_eval(ptr, int32(len(src)), mode))
}

// Value returns what the last ModeValue evaluation streamed back.
func (a *ABI) Value() (any, error) {
	return a.dec.result()
}

func (a *ABI) Output() string {
	if a.mod == nil {
		return ""
	}
	return a.str(a.mod.Xmp_api_out(), a.mod.Xmp_api_out_len())
}

// Func resolves a global callable to a handle, so repeated calls do not
// re-intern the name and re-walk the globals.
func (a *ABI) Func(name string) (handle int32, err error) {
	if err := a.status(); err != nil {
		return 0, err
	}
	defer a.guard(&err)

	ptr, err := a.WriteString(name)
	if err != nil {
		return 0, err
	}

	handle = a.mod.Xmp_api_func(ptr, int32(len(name)))
	if handle < 0 {
		if err := a.lastError(false); err != nil {
			return 0, err
		}
		return 0, value.NewException("NameError", fmt.Sprintf("cannot resolve %q", name))
	}
	return handle, nil
}

// Call invokes a handle. Arguments go over encoded in one buffer, so the whole
// invocation is a single crossing.
func (a *ABI) Call(handle int32, args []any) (_ any, err error) {
	if err := a.status(); err != nil {
		return nil, err
	}
	defer a.guard(&err)

	a.dec.reset()
	ptr, encoded, err := a.WriteArgs(args)
	if err != nil {
		return nil, err
	}

	if err := a.check(a.mod.Xmp_api_call(handle, ptr, encoded, int32(len(args)))); err != nil {
		return nil, err
	}
	return a.dec.result()
}

// check turns a non-zero return code into the traceback the module recorded.
func (a *ABI) check(rc int32) error {
	if rc == apiOK {
		return nil
	}

	interrupted := a.cancelled.Load()

	if err := a.lastError(interrupted); err != nil {
		return err
	}
	if interrupted {
		return ErrInterrupted
	}
	return value.NewException("RuntimeError", "unknown error")
}

// lastError returns the exception the module recorded, or nil if there is
// none.
//
// The traceback text is already waiting in the module's error buffer; the
// structure has to be asked for, because walking the exception runs Python and
// the module will not do that while it is still unwinding. The walk reuses the
// value decoder, which is free at this point -- the call it belonged to failed,
// so there is no result to collect.
func (a *ABI) lastError(interrupted bool) *value.Exception {
	text := a.str(a.mod.Xmp_api_err(), a.mod.Xmp_api_err_len())

	var streamed any
	a.dec.reset()
	if a.mod.Xmp_api_err_value() == apiOK {
		if v, err := a.dec.result(); err == nil {
			streamed = v
		}
	}
	a.dec.reset()

	exc := value.FromGuest(text, streamed, interrupted)

	if exc.Raw() == "" && exc.Type() == "" {
		return nil
	}
	return exc
}
