package micropython

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/gregfurman/micropython-go/internal/api"
	"github.com/gregfurman/micropython-go/internal/host"
	"github.com/gregfurman/micropython-go/internal/value"
)

// HostFunc is a Go function callable from Python through [WithHostFunc] or
// [Instance.DefineFunction]. Errors and panics become Python exceptions;
// use [Raise] to choose the exception class.
// A callback must not synchronously call or close its own Instance.
type HostFunc func(ctx context.Context, args []Value) (Value, error)

var (
	// ErrClosed reports a closed interpreter or pool.
	ErrClosed = api.ErrClosed

	// ErrInterrupted identifies an interrupted call.
	ErrInterrupted = api.ErrInterrupted

	// ErrInstanceNotInitialised reports an Instance that was not initialized.
	ErrInstanceNotInitialised = errors.New("cannot perform operation on Instance that has not been initialised")
)

// Instance is a MicroPython interpreter whose state persists between calls.
// It is safe for concurrent use, but executes one operation at a time.
// Use [Program] or [Instance.Clone] for parallel execution, and call Close when done.
type Instance struct {
	wrapped *api.Instance
}

// NewInstance creates an interpreter and applies its initialization options.
// The caller must close it when done.
func NewInstance(ctx context.Context, opts ...Option) (*Instance, error) {
	opt := newOptions(opts)
	return newInstance(ctx, opt)
}

func newInstance(ctx context.Context, opt *options) (*Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := opt.validate(); err != nil {
		return nil, err
	}

	networkConfig, err := opt.network()
	if err != nil {
		return nil, err
	}
	in, err := api.New(int32(opt.heapBytes), opt.stdout, networkConfig, opt.filesystem, opt.vars)
	if err != nil {
		return nil, err
	}

	for _, key := range slices.Sorted(maps.Keys(opt.globals)) {
		if err := in.Set(ctx, key, unwrapAny(opt.globals[key])); err != nil {
			in.Close()
			return nil, err
		}
	}

	for _, name := range slices.Sorted(maps.Keys(opt.hostFuncs)) {
		if err := in.DefineFunction(ctx, name, hostFunc(opt.hostFuncs[name])); err != nil {
			in.Close()
			return nil, err
		}
	}

	if src := opt.sourceScript; src != "" {
		if err := in.Exec(ctx, src); err != nil {
			in.Close()
			return nil, err
		}
	}

	return &Instance{wrapped: in}, nil
}

// Set binds v to a Python global. It accepts Go values and [Value] builders,
// using the same conversion rules as [Instance.Call].
func (i *Instance) Set(ctx context.Context, name string, v any) error {
	if i.wrapped == nil {
		return ErrInstanceNotInitialised
	}
	return i.wrapped.Set(ctx, name, unwrapAny(v))
}

// Get reads a Python global without evaluating an expression.
// It returns a Python NameError if the name is unbound.
func (i *Instance) Get(ctx context.Context, name string) (Value, error) {
	if i.wrapped == nil {
		return Value{}, ErrInstanceNotInitialised
	}

	out, err := i.wrapped.Get(ctx, name)
	if err != nil {
		return Value{}, err
	}

	return wrapValue(out), nil
}

// Resolve reads an object handle using the same conversion rules as Eval.
// Containers are copied; cyclic or deeply nested parts remain handles.
// Other objects return another handle to the same object.
func (i *Instance) Resolve(ctx context.Context, v Value) (Value, error) {
	if i.wrapped == nil {
		return Value{}, ErrInstanceNotInitialised
	}

	obj, ok := v.val.(value.Object)
	if !ok {
		return Value{}, conversionError(v, "object")
	}

	out, err := i.wrapped.Resolve(ctx, obj)
	if err != nil {
		return Value{}, err
	}

	return wrapValue(out), nil
}

// Release unpins guest objects, including handles nested in containers.
// It invalidates all handles to each object, even separately acquired ones.
// Non-handles, already-released handles, and foreign handles are ignored.
//
// Release does not run GC or remove Python-owned references. It is optional:
// Go cleanup also queues releases, applied by subsequent interpreter operations.
func (i *Instance) Release(ctx context.Context, vals ...Value) error {
	if i.wrapped == nil {
		return ErrInstanceNotInitialised
	}

	var refs []*value.Ref
	for _, val := range vals {
		walk(val, func(v Value) bool {
			if obj, err := v.AsObject(); err == nil {
				refs = append(refs, obj.handle())
			}
			return true
		})
	}

	if len(refs) == 0 {
		return nil
	}

	return i.wrapped.Release(ctx, refs...)
}

// DefineFunction binds fn to a Python global, replacing any existing binding.
// The binding persists across calls and is inherited by clones.
// See [HostFunc] for callback behavior.
func (i *Instance) DefineFunction(ctx context.Context, name string, fn HostFunc) error {
	if i.wrapped == nil {
		return ErrInstanceNotInitialised
	}

	return i.wrapped.DefineFunction(ctx, name, hostFunc(fn))
}

// hostFunc adapts a public HostFunc to the value model the guest speaks. The
// result is unwrapped here rather than encoded as the Value wrapper, which the
// codec would otherwise take for an ordinary Go struct.
func hostFunc(fn HostFunc) host.HostFunc {
	if fn == nil {
		return nil
	}

	return func(ctx context.Context, args []value.Value) (value.Value, error) {
		result, err := fn(ctx, wrapValues(args))
		if err != nil {
			return nil, err
		}

		return result.val, nil
	}
}

// Cancel requests a KeyboardInterrupt in the current Python execution.
// It is safe from any goroutine and does not affect the next operation.
// Interruption is best effort: long C-level operations may delay it.
func (i *Instance) Cancel() error {
	if i.wrapped == nil {
		return ErrInstanceNotInitialised
	}
	i.wrapped.Cancel()
	return nil
}

// Call invokes a Python global function. Arguments may be Go values or [Value]
// builders; the result is a Value. Python state changes persist after the call.
func (i *Instance) Call(ctx context.Context, name string, args ...any) (Value, error) {
	if i.wrapped == nil {
		return Value{}, ErrInstanceNotInitialised
	}

	out, err := i.wrapped.Call(ctx, name, unwrapArgs(args)...)
	if err != nil {
		return Value{}, err
	}

	return wrapValue(out), nil
}

// Clone copies the current Python state into a new, caller-owned Instance.
// It briefly locks the source. Go callback closures and output writers are shared;
// guest handles cannot be transferred between instances.
// Close Python sockets, files, and directory iterators before cloning.
func (i *Instance) Clone(ctx context.Context) (*Instance, error) {
	if i.wrapped == nil {
		return nil, ErrInstanceNotInitialised
	}

	snap, err := i.wrapped.Snapshot(ctx)
	if err != nil {
		return nil, err
	}

	return fromSnapshot(snap)
}

// Close interrupts active execution and closes the interpreter.
// Later execution attempts return [ErrClosed].
func (i *Instance) Close() error {
	if i.wrapped == nil {
		return ErrInstanceNotInitialised
	}
	return i.wrapped.Close()
}

// Eval evaluates a Python expression and returns its Value.
// Use [Instance.Exec] for statements and assignments.
func (i *Instance) Eval(ctx context.Context, expr string) (Value, error) {
	if i.wrapped == nil {
		return Value{}, ErrInstanceNotInitialised
	}

	out, err := i.wrapped.Eval(ctx, expr)
	if err != nil {
		return Value{}, err
	}

	return wrapValue(out), nil
}

// Exec runs Python statements, preserving their state changes.
// Output goes to [WithStdout], or is discarded by default.
func (i *Instance) Exec(ctx context.Context, src string) error {
	if i.wrapped == nil {
		return ErrInstanceNotInitialised
	}
	return i.wrapped.Exec(ctx, src)
}

// Err returns the interpreter's fatal error or [ErrClosed], or nil if healthy.
func (i *Instance) Err() error {
	if i.wrapped == nil {
		return ErrInstanceNotInitialised
	}
	return i.wrapped.Err()
}

func fromSnapshot(s *api.Snapshot) (*Instance, error) {
	instance, err := api.FromSnapshot(s)
	if err != nil {
		return nil, err
	}
	return &Instance{wrapped: instance}, nil
}

func (i *Instance) restore(s *api.Snapshot) error {
	if i.wrapped == nil {
		return ErrInstanceNotInitialised
	}
	return i.wrapped.Restore(context.Background(), s)
}
