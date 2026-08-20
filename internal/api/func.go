package api

type Func struct {
	in     *Instance
	handle int32
	name   string
	gen    uint64
}

func (i *Instance) Func(name string) (*Func, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.abi == nil {
		return nil, ErrClosed
	}
	handle, err := i.abi.Func(name)
	if err != nil {
		return nil, err
	}

	// TODO: cancel func if closed early
	return &Func{in: i, handle: handle, name: name, gen: i.gen}, nil
}

func (i *Instance) define(name, src string) (*Func, error) {
	if _, err := i.Exec(src); err != nil {
		return nil, err
	}
	return i.Func(name)
}

func (f *Func) Name() string { return f.name }

// Call invokes the function and returns its result as a native Go value, using
// the same mapping as Eval.
func (f *Func) Call(args ...any) (any, error) {
	i := f.in
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.abi == nil {
		return nil, ErrClosed
	}

	if f.gen != i.gen {
		return nil, ErrStale
	}
	return i.abi.Call(f.handle, args)
}
