package host

import (
	"fmt"

	"github.com/gregfurman/micropython-wasi/internal/env"
	wasi "github.com/gregfurman/micropython-wasi/internal/micropython"
)

// The module's exports, typed.

// Must match build/wasm_api.h.
const (
	ModeExec  = 0 // run as a script, the result is whatever it printed
	ModeValue = 1 // evaluate one expression, streamed back through val_*
)

const apiOK = 0

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
		if err := a.lastError(); err != nil {
			return 0, err
		}
		return 0, &Exception{Message: fmt.Sprintf("micropython: cannot resolve %q", name)}
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

	if err := a.lastError(); err != nil {
		err.interrupted = interrupted
		return err
	}
	if interrupted {
		return ErrInterrupted
	}
	return &Exception{Message: "micropython: unknown error"}
}

// lastError returns the exception the module recorded, or nil if there is
// none.
//
// The traceback text is already waiting in the module's error buffer; the
// structure has to be asked for, because walking the exception runs Python and
// the module will not do that while it is still unwinding. The walk reuses the
// value decoder, which is free at this point -- the call it belonged to failed,
// so there is no result to collect.
func (a *ABI) lastError() *Exception {
	text := a.str(a.mod.Xmp_api_err(), a.mod.Xmp_api_err_len())

	exc := &Exception{Raw: text}

	a.dec.reset()
	if a.mod.Xmp_api_err_value() == apiOK {
		if v, err := a.dec.result(); err == nil {
			exc.fill(v)
		}
	}
	a.dec.reset()

	if exc.Raw == "" && exc.Type == "" {
		return nil
	}
	return exc
}
