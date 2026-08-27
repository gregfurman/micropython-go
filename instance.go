package micropython

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/gregfurman/micropython-go/internal/api"
	"github.com/gregfurman/micropython-go/internal/host"
)

// HostFunc is a Go function callable from Python. See Instance.DefineFunction.
type HostFunc func(args []any) (any, error)

var (
	// ErrClosed indicates the interpreter or pool has been shut down and can no longer be used.
	ErrClosed = api.ErrClosed

	// ErrInterrupted indicates the guest execution was halted, either by context cancellation or a manual Cancel call.
	ErrInterrupted = api.ErrInterrupted

	// ErrInstanceNotInitialised is returned if an operation is attempted on an improperly constructed Instance.
	ErrInstanceNotInitialised = errors.New("cannot perform operation on Instance that has not been initialised")
)

// Instance represents a single, stateful MicroPython interpreter.
//
// Unlike a Program, an Instance maintains state between calls. Variables, imports,
// and function definitions created during one execution will persist and remain
// available for subsequent calls.
//
// An Instance is safe for concurrent use across multiple goroutines, but it is
// strictly sequential. Because it operates on a single linear WebAssembly memory,
// concurrent calls will queue and execute one at a time. If you need parallel
// execution, use a Program or Clone this instance.
type Instance struct {
	in *api.Instance
}

// NewInstance boots a fresh MicroPython interpreter.
//
// If any globals are provided via options, they are injected into the Python
// environment immediately upon startup.
func NewInstance(ctx context.Context, opts ...option) (*Instance, error) {
	opt := newOptions(opts)

	instance, err := api.New(opt.heapBytes)
	if err != nil {
		return nil, err
	}

	for _, key := range slices.Sorted(maps.Keys(opt.globals)) {
		if err := instance.Set(ctx, key, opt.globals[key].val); err != nil {
			instance.Close()
			return nil, err
		}
	}

	return &Instance{in: instance}, nil
}

// Set directly binds a Go value to a global Python variable by name, bypassing
// the need to parse source text.
func (i *Instance) Set(ctx context.Context, name string, v Value) error {
	if i.in == nil {
		return ErrInstanceNotInitialised
	}
	return i.in.Set(ctx, name, v.val)
}

// DefineFunction binds a Go function to a global Python name, letting the guest
// call back into the host.
//
// Arguments arrive already converted to native Go values, following the same
// rules as the Type Conversion table in reverse; a []any holds a Python list or
// tuple, and a map[any]any holds a dict. The returned value is converted back
// using those same rules, so a Value built with Of, Tuple, Str and friends
// controls the Python type precisely.
//
// Returning an error raises an exception at the Python call site: HostError
// carrying the error's text, or a specific builtin class if the error came from
// Raise. HostError subclasses RuntimeError, so guest code can catch host-side
// failures specifically with "except HostError" while a general
// "except RuntimeError" still sees them. A panic inside fn is recovered and
// raised the same way rather than unwinding into the interpreter.
//
// The binding is part of interpreter state, so it persists for the life of the
// Instance and is inherited by any Clone taken afterwards. Redefining a name
// replaces it.
func (i *Instance) DefineFunction(ctx context.Context, name string, fn HostFunc) error {
	if i.in == nil {
		return ErrInstanceNotInitialised
	}
	return i.in.DefineFunction(ctx, name, host.HostFunc(fn))
}

// Cancel interrupts any Python execution currently in flight on this instance.
// The running code receives a KeyboardInterrupt.
//
// Safe from any goroutine. Cancelling an idle interpreter is also safe but does
// nothing: the request is cleared when the next call begins, so it cannot make
// a later call fail.
//
// It is best effort. The request lands at the next VM hook, so a guest inside
// one long C-level operation -- a regex match, a big-int multiply, a sort --
// does not stop until that finishes.
func (i *Instance) Cancel() error {
	if i.in == nil {
		return ErrInstanceNotInitialised
	}
	i.in.Cancel()
	return nil
}

// Call invokes a Python global function by name, passing the provided arguments,
// and returns its result translated into a native Go value.
//
// Because the Instance is stateful, the Python function may interact with or mutate
// globals that persist after the Call returns.
func (i *Instance) Call(ctx context.Context, name string, args ...any) (any, error) {
	if i.in == nil {
		return nil, ErrInstanceNotInitialised
	}
	return i.in.Call(ctx, name, unwrapArgs(args)...)
}

// Clone captures a snapshot of the interpreter's current memory and returns a completely
// independent Instance starting from that exact state.
//
// Cloning momentarily locks the underlying interpreter while the memory is copied,
// meaning no other calls can execute until the clone is complete.
func (i *Instance) Clone(ctx context.Context) (*Instance, error) {
	if i.in == nil {
		return nil, ErrInstanceNotInitialised
	}

	snap, err := i.in.Snapshot(ctx)
	if err != nil {
		return nil, err
	}

	return fromSnapshot(snap)
}

// Close gracefully tears down the instance, interrupting any currently executing
// logic and freeing the underlying WebAssembly memory.
//
// Subsequent operations on this Instance will return ErrClosed.
func (i *Instance) Close() error {
	if i.in == nil {
		return ErrInstanceNotInitialised
	}
	return i.in.Close()
}

// Eval evaluates a single Python expression (e.g., "1 + 1" or "my_dict['key']")
// and returns the resulting native Go value.
//
// Unlike Exec, Eval cannot execute multi-line statements or variable assignments.
func (i *Instance) Eval(ctx context.Context, expr string) (any, error) {
	if i.in == nil {
		return nil, ErrInstanceNotInitialised
	}
	return i.in.Eval(ctx, expr)
}

// Exec runs arbitrary Python source code as a script and returns whatever the
// script printed to stdout.
//
// Variables, imports, and functions defined during Exec will remain available
// in the Instance for future calls to Eval, Exec, or Call.
func (i *Instance) Exec(ctx context.Context, src string) (string, error) {
	if i.in == nil {
		return "", ErrInstanceNotInitialised
	}
	return i.in.Exec(ctx, src)
}

// Err reports whether the interpreter is in an unrecoverable state.
//
// It returns nil if the instance is healthy. If the WebAssembly VM experienced
// a fatal trap (like memory corruption) or the instance was explicitly Closed,
// this returns the corresponding error.
func (i *Instance) Err() error {
	if i.in == nil {
		return ErrInstanceNotInitialised
	}
	return i.in.Err()
}

func fromSnapshot(s *api.Snapshot) (*Instance, error) {
	instance, err := api.FromSnapshot(s)
	if err != nil {
		return nil, err
	}
	return &Instance{in: instance}, nil
}

func (i *Instance) restore(s *api.Snapshot) error {
	if i.in == nil {
		return ErrInstanceNotInitialised
	}
	return i.in.Restore(context.Background(), s)
}
