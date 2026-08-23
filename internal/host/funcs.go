package host

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Func is one Go function exposed to Python.
type Func struct {
	Name string
	Doc  string
	call func(*Call) (any, error)
}

// attr answers one attribute lookup, reporting false for a name it does not
// have, which is what makes the guest raise AttributeError.
func (f *Func) attr(name string) (any, bool) {
	switch name {
	case "__name__":
		return f.Name, true
	case "__doc__":
		if f.Doc == "" {
			return nil, true // None, as an undocumented Python function has
		}
		return f.Doc, true
	}
	return nil, false
}

// Registry is the set of host functions an interpreter is built with. The zero
// Registry is empty and usable, and so is a nil one.
type Registry struct {
	funcs []*Func
}

func (r *Registry) Add(f *Func) {
	r.funcs = append(r.funcs, f)
}

func (r *Registry) at(id int32) *Func {
	if r == nil || id < 0 || int(id) >= len(r.funcs) {
		return nil
	}
	return r.funcs[id]
}

func (r *Registry) all() []*Func {
	if r == nil {
		return nil
	}
	return r.funcs
}

// -----------------------------------------------------------

// Xcall_begin puts aside the decode in progress, so the arguments about to
// arrive are assembled on their own. See the note in wasm_host.c.
func (a *ABI) Xcall_begin() {
	a.saved = append(a.saved, a.dec)
	a.dec = decoder{}
}

// Xcall_end runs the function and returns where the guest can read its reply,
// or 0 if there is nothing to read.
//
// A failure is a reply too: it comes back as an exception for the guest to
// raise, so 0 is reserved for the case where the host could not even pack that.
func (a *ABI) Xcall_end(id int32) int32 {
	args, decErr := a.dec.result()

	if n := len(a.saved); n > 0 {
		a.dec, a.saved[n-1] = a.saved[n-1], decoder{}
		a.saved = a.saved[:n-1]
	} else {
		a.dec.reset()
	}

	return a.reply(a.invoke(id, args, decErr))
}

// Xattr answers an attribute lookup on a host function, or returns 0 for a
// name it does not have.
func (a *ABI) Xattr(id, ptr, length int32) int32 {
	fn := a.funcs.at(id)
	if fn == nil {
		return 0
	}
	value, ok := fn.attr(a.str(ptr, length))
	if !ok {
		return 0
	}
	return a.reply(value, nil)
}

// invoke runs one host function, turning anything that goes wrong -- including
// a panic in the caller's own code -- into an error the guest will raise.
func (a *ABI) invoke(id int32, packed any, decErr error) (_ any, err error) {
	if decErr != nil {
		return nil, fmt.Errorf("micropython: bad arguments to host function: %w", decErr)
	}

	fn := a.funcs.at(id)
	if fn == nil {
		return nil, fmt.Errorf("micropython: no host function with id %d", id)
	}

	args, kwargs, err := unpair(packed)
	if err != nil {
		return nil, err
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s() panic: %v", fn.Name, r)
		}
	}()
	return fn.call(&Call{name: fn.Name, args: args, kwargs: kwargs})
}

func unpair(v any) ([]any, map[string]any, error) {
	pair, ok := v.(Tuple)
	if !ok || len(pair) != 2 {
		return nil, nil, fmt.Errorf("micropython: host call sent %T, want an (args, kwargs) pair", v)
	}
	args, ok := pair[0].(Tuple)
	if !ok {
		return nil, nil, fmt.Errorf("micropython: host call sent %T positional arguments", pair[0])
	}

	// Keywords have string keys and nothing else, so they always decode to a
	// map[string]any -- including the empty one.
	kwargs, _ := pair[1].(map[string]any)
	return args, kwargs, nil
}

// reply packs the outcome into the guest's reply buffer and returns its
// address.
func (a *ABI) reply(value any, err error) int32 {
	a.rep.reset()
	// The length goes in front, and is not known until the value has been
	// packed. Reserving it by writing the same u32 that will overwrite it
	// leaves nothing to keep in step: the header is exactly as wide as the
	// write that fills it, whatever that width is.
	a.rep.u32(0)
	header := len(a.rep.buf)

	if err == nil {
		if encErr := a.rep.value(value, 0); encErr != nil {
			err = encErr
		}
	}

	if err != nil {
		// An *Exception names the Python exception to raise, so a host
		// function can hand back a KeyError the guest catches as one.
		kind, message := "RuntimeError", err.Error()
		var exc *Exception
		if errors.As(err, &exc) && exc.Type != "" {
			kind, message = exc.Type, exc.Message
		}

		a.rep.buf = a.rep.buf[:header]
		a.rep.tag(pkException)
		a.rep.tag(pkStr)
		a.rep.blob(kind)
		a.rep.tag(pkStr)
		a.rep.blob(message)
	}

	binary.LittleEndian.PutUint32(a.rep.buf, uint32(len(a.rep.buf)-header))

	return a.copyOut(a.rep.buf)
}

// copyOut hands the bytes to the guest through the buffer it keeps for
// replies, which is separate from the argument scratch so the two cannot
// collide. Calling back into the module from inside one of its own imports is
// exactly what the guest would do calling the export itself.
func (a *ABI) copyOut(b []byte) int32 {
	ptr := a.mod.Xmp_api_reply(int32(len(b)))
	dst, ok := a.slice(ptr, int32(len(b)))
	if ptr <= 0 || !ok {
		return 0
	}
	copy(dst, b)
	return ptr
}
