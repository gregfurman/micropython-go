package exec

import "fmt"

// Func is a Python callable that has already been looked up.
//
// This is the shape to use when the same function is invoked repeatedly
// against different inputs: resolving the name once lifts the qstr interning
// and the global lookup out of the call, leaving only argument marshalling,
// the call itself, and streaming the result back.
//
// A Func belongs to the Instance that produced it, and inherits its
// serial-use-only rule.
type Func struct {
	in     *Instance
	handle int32
	name   string
}

// Func resolves a global callable by name.
func (i *Instance) Func(name string) (*Func, error) {
	ptr, err := i.writeScratch(name)
	if err != nil {
		return nil, err
	}

	handle := i.mod.Xmp_api_func(ptr, int32(len(name)))
	if handle < 0 {
		text := i.readString(i.mod.Xmp_api_err(), i.mod.Xmp_api_err_len())
		if text == "" {
			text = fmt.Sprintf("cannot resolve %q", name)
		}
		return nil, &Error{Text: text}
	}
	return &Func{in: i, handle: handle, name: name}, nil
}

// Define runs src, which is expected to define name, and returns a handle to
// it. This is the one-time setup for the call-many pattern:
//
//	fn, err := in.Define("score", `
//	def score(row):
//	    return row["a"] * 2 + row["b"]
//	`)
//	for _, row := range rows {
//	    v, err := fn.Call(row)
//	}
func (i *Instance) Define(name, src string) (*Func, error) {
	if _, err := i.Exec(src); err != nil {
		return nil, err
	}
	return i.Func(name)
}

// Name returns the name the function was resolved under.
func (f *Func) Name() string { return f.name }

// Call invokes the function and returns its result as a native Go value, using
// the same mapping as Eval.
func (f *Func) Call(args ...any) (any, error) {
	in := f.in
	in.values.reset()

	// Pack every argument into one buffer, so the whole invocation is a single
	// crossing rather than one per scalar.
	in.packer.reset()
	for _, arg := range args {
		if err := in.packer.value(arg, 0); err != nil {
			return nil, err
		}
	}

	ptr, err := in.writeScratchBytes(in.packer.buf)
	if err != nil {
		return nil, err
	}

	rc := in.mod.Xmp_api_call(f.handle, ptr, int32(len(in.packer.buf)), int32(len(args)))
	if err := in.check(rc); err != nil {
		return nil, err
	}
	return in.values.result()
}

// Output returns whatever the last call printed.
func (f *Func) Output() string { return f.in.out() }
