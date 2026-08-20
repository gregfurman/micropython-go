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

// Error is a Python exception that escaped to the host, carrying the traceback
// as MicroPython formatted it.
type Error struct {
	Text string
}

func (e *Error) Error() string { return e.Text }

// New instantiates the module and wraps it. The ABI has to build the module
// rather than be handed one: wasi.New takes the val_* implementation as its
// import object, and that is the ABI itself.
func New() *ABI {
	a := &ABI{}
	a.mod = wasi.New(env.New(), a)
	// One of the module's constructors boots the interpreter.
	a.mod.X_initialize()
	return a
}

// Eval runs src. In ModeValue the result is available from Value.
func (a *ABI) Eval(src string, mode int32) error {
	ptr, err := a.WriteString(src)
	if err != nil {
		return err
	}
	a.begin()
	a.dec.reset()
	return a.check(a.mod.Xmp_api_eval(ptr, int32(len(src)), mode))
}

// Value returns what the last ModeValue evaluation streamed back.
func (a *ABI) Value() (any, error) { return a.dec.result() }

func (a *ABI) Output() string {
	return a.ReadString(a.mod.Xmp_api_out(), a.mod.Xmp_api_out_len())
}

// Func resolves a global callable to a handle, so repeated calls do not
// re-intern the name and re-walk the globals.
func (a *ABI) Func(name string) (int32, error) {
	ptr, err := a.WriteString(name)
	if err != nil {
		return 0, err
	}

	handle := a.mod.Xmp_api_func(ptr, int32(len(name)))
	if handle < 0 {
		if err := a.lastError(); err != nil {
			return 0, err
		}
		return 0, &Error{Text: fmt.Sprintf("cannot resolve %q", name)}
	}
	return handle, nil
}

// Call invokes a handle. Arguments go over encoded in one buffer, so the whole
// invocation is a single crossing.
func (a *ABI) Call(handle int32, args []any) (any, error) {
	a.begin()
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

func (a *ABI) check(rc int32) error {
	if rc == apiOK {
		return nil
	}
	if err := a.lastError(); err != nil {
		return err
	}
	return &Error{Text: "unknown error"}
}

// lastError returns the traceback the module recorded, or nil if there is
// none.
func (a *ABI) lastError() error {
	text := a.ReadString(a.mod.Xmp_api_err(), a.mod.Xmp_api_err_len())
	if text == "" {
		return nil
	}
	return &Error{Text: text}
}
