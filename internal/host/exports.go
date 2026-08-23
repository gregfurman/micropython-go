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

// New boots an interpreter with funcs bound into its globals. A nil Registry
// means no host functions.
func New(funcs *Registry) (*ABI, error) {
	a := newABI(funcs)
	if err := a.init(); err != nil {
		return nil, err
	}

	for id, fn := range funcs.all() {
		if err := a.register(fn.Name, int32(id)); err != nil {
			return nil, fmt.Errorf("micropython: cannot bind %s: %w", fn.Name, err)
		}
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
func newABI(funcs *Registry) *ABI {
	a := &ABI{funcs: funcs}
	a.mod = wasi.New(env.New(), a)
	a.base = *a.mod.X__stack_pointer()
	return a
}

// register binds a host function to a global name. Only New calls it: the
// binding lands in the globals a snapshot copies, so every interpreter
// restored from one already has it.
func (a *ABI) register(name string, id int32) (err error) {
	defer a.guard(&err)

	ptr, err := a.WriteString(name)
	if err != nil {
		return err
	}
	return a.check(a.mod.Xmp_api_register(ptr, int32(len(name)), id))
}

// Set binds a value to a global name, so a host can seed configuration into an
// interpreter without going through source text.
func (a *ABI) Set(name string, value any) (err error) {
	if err := a.status(); err != nil {
		return err
	}
	defer a.guard(&err)

	a.enc.reset()
	a.enc.buf = append(a.enc.buf, name...)
	if err := a.enc.value(value, 0); err != nil {
		return err
	}

	ptr, err := a.Write(a.enc.buf)
	if err != nil {
		return err
	}

	n := int32(len(name))
	return a.check(a.mod.Xmp_api_set(ptr, n, ptr+n, int32(len(a.enc.buf))-n))
}

func (a *ABI) init() (err error) {
	defer a.guard(&err)
	a.mod.X_initialize()
	return nil
}

// newEpoch drops every object reference the guest was holding for the host and
// starts a new generation, invalidating the Objects handed out under the last
// one.
//
// This is where the design differs from proxy_c.c, which frees each reference
// individually from a JavaScript finaliser. Nothing here can rely on a
// finaliser running, and a pooled Program lays a snapshot back down between
// calls -- after which an index means a different object, or none. So the
// lifetime is the call, and it is ended explicitly.
func (a *ABI) newEpoch() {
	a.epoch++
	a.enc.epoch = a.epoch
	a.rep.epoch = a.epoch
	a.mod.Xmp_api_refs_clear()
}

// Eval runs src. In ModeValue the result is available from Value.
func (a *ABI) Eval(src string, mode int32) (err error) {
	if err := a.status(); err != nil {
		return err
	}
	defer a.guard(&err)

	a.newEpoch()
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

	a.newEpoch()
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

// callRef invokes a reference the guest is holding for the host.
//
// It runs inside a call that is already in flight -- a host function reached
// from Python -- so it nests the same way a host call does, on the decoder
// that call is using, and leaves the guest's own C stack bookkeeping alone.
func (a *ABI) callRef(abi *ABI, ref int32, epoch uint64, args []any) (_ any, err error) {
	if err := a.status(); err != nil {
		return nil, err
	}
	if abi != a || epoch != a.epoch {
		return nil, fmt.Errorf("micropython: object reference is no longer live")
	}
	defer a.guard(&err)

	a.saved = append(a.saved, a.dec)
	a.dec = decoder{}
	defer func() {
		n := len(a.saved) - 1
		a.dec, a.saved[n] = a.saved[n], decoder{}
		a.saved = a.saved[:n]
	}()

	ptr, encoded, err := a.WriteArgs(args)
	if err != nil {
		return nil, err
	}

	if err := a.check(a.mod.Xmp_api_ref_call(ref, ptr, encoded, int32(len(args)))); err != nil {
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

	exc := &Exception{raw: text}

	a.dec.reset()
	if a.mod.Xmp_api_err_value() == apiOK {
		if v, err := a.dec.result(); err == nil {
			exc.fill(v)
		}
	}
	a.dec.reset()

	if exc.Raw() == "" && exc.Type == "" {
		return nil
	}
	return exc
}
