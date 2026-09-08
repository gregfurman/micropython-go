package micropython

import (
	"fmt"
	"io"
	"math"
)

type options struct {
	maxIdle    int
	maxIdleSet bool
	heapBytes  int64

	globals      map[string]any
	hostFuncs    map[string]HostFunc
	sourceScript string
	stdout       io.Writer
}

// ProgramOption configures a Program. Every Option is also a ProgramOption.
type ProgramOption interface {
	apply(*options)
}

// Option configures an Instance or a Program.
type Option interface {
	ProgramOption
	instanceOption()
}

type optionFunc func(*options)

func (f optionFunc) apply(o *options) { f(o) }
func (optionFunc) instanceOption()    {}

type programOptionFunc func(*options)

func (f programOptionFunc) apply(o *options) { f(o) }

// WithHeapSize sets the Python heap size in bytes; zero uses the default.
// Invalid sizes fail at construction. Exhausting the heap raises MemoryError.
func WithHeapSize(bytes int) Option {
	return optionFunc(func(o *options) {
		o.heapBytes = int64(bytes)
	})
}

// WithMaxIdle limits idle interpreters retained by a Program, not active runs.
// Zero uses runtime.NumCPU; negative values fail at compilation.
func WithMaxIdle(n int) ProgramOption {
	return programOptionFunc(func(o *options) {
		o.maxIdle = n
		o.maxIdleSet = true
	})
}

// WithHostFunc binds fn to a Python global before source execution.
// Repeated names use the last binding. Programs and clones share the Go closure,
// which must support concurrent calls; its state is not rewound. See [HostFunc].
func WithHostFunc(name string, fn HostFunc) Option {
	return optionFunc(func(o *options) {
		if o.hostFuncs == nil {
			o.hostFuncs = make(map[string]HostFunc, 1)
		}
		o.hostFuncs[name] = fn
	})
}

// WithSource runs src after binding globals and host functions at initialization.
// A nonempty source argument to [Compile] overrides this option.
func WithSource(src string) Option {
	return optionFunc(func(o *options) {
		o.sourceScript = src
	})
}

// Globals maps Python global names to initial Go values for [WithGlobals].
type Globals = map[string]any

// WithGlobals sets initial Python globals using [Instance.Set] conversion rules.
// Repeated use replaces the entire map.
func WithGlobals(g Globals) Option {
	return optionFunc(func(o *options) {
		o.globals = g
	})
}

func newOptions[T ProgramOption](opts []T) *options {
	o := &options{}
	for _, opt := range opts {
		opt.apply(o)
	}
	return o
}

// validate reports a configuration that cannot be honoured, rather than
// narrowing it silently. A heap size is an int on the way in and an int32 on
// the way down, so a 64-bit value has to be checked before it is cast.
func (o *options) validate() error {
	switch {
	case o.heapBytes < 0:
		return fmt.Errorf("micropython: heap size %d is negative", o.heapBytes)
	case o.heapBytes > math.MaxInt32:
		return fmt.Errorf("micropython: heap size %d exceeds the %d byte maximum", o.heapBytes, math.MaxInt32)
	case o.maxIdleSet && o.maxIdle < 0:
		return fmt.Errorf("micropython: idle interpreter count %d is negative", o.maxIdle)
	}
	return nil
}

// WithStdout directs Python output to w; the default is io.Discard.
// The caller owns w. Programs and clones share it without synchronization,
// so it must support concurrent writes when used concurrently.
func WithStdout(w io.Writer) Option {
	return optionFunc(func(o *options) {
		o.stdout = w
	})
}
