package micropython

import (
	"io"
)

type options struct {
	programPoolSize int
	heapBytes       int32

	globals      map[string]Value
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

// WithHeapSize sets the interpreter's Python heap, in bytes. Zero takes the
// module's default.
//
// The heap is most of what an interpreter costs: creating one and rewinding it
// between calls are both proportional to it, so a program that does not need
// much is markedly cheaper with less. Too little and the guest raises
// MemoryError.
func WithHeapSize(bytes int) Option {
	return optionFunc(func(o *options) {
		o.heapBytes = int32(bytes)
	})
}

// WithPoolSize sets how many idle interpreters a Program keeps between calls.
// Zero takes runtime.NumCPU.
//
// It bounds what is kept, not what exists. A call that finds none idle builds
// one, and the surplus is closed on release rather than refused, so peak memory
// follows concurrency rather than n:
//
//	WithPoolSize(4)  // a burst of 256 concurrent calls still builds 256
//
// Each interpreter holds its own linear memory, roughly 1.2 MiB plus
// WithHeapSize. Set n to the concurrency you expect.
func WithPoolSize(n int) ProgramOption {
	return programOptionFunc(func(o *options) {
		o.programPoolSize = n
	})
}

// WithHostFunc binds a Go function to a global Python name before the source
// runs, so module-level code can call it. Equivalent to DefineFunction, except
// that a Program registers it ahead of its snapshot and so keeps it across
// calls; a Program has no other way to define one.
//
// Repeated use accumulates. Binding the same name twice keeps the last.
//
// A Program registers fn once, before the snapshot, so every pooled interpreter
// shares that one closure. What fn closes over is Go state: the per-call rewind
// does not reset it, and pooled interpreters call it in parallel.
//
//	var n atomic.Int64 // atomic: called from several interpreters at once
//	p, _ := micropython.CompileSource(ctx, "def f(): return tick()",
//	    micropython.WithHostFunc("tick", func([]any) (any, error) {
//	        return n.Add(1), nil
//	    }))
//	p.Call(ctx, "f") // 1
//	p.Call(ctx, "f") // 2, not rewound to 1
func WithHostFunc(name string, fn HostFunc) Option {
	return optionFunc(func(o *options) {
		if o.hostFuncs == nil {
			o.hostFuncs = make(map[string]HostFunc, 1)
		}
		o.hostFuncs[name] = fn
	})
}

// WithSourceScript runs src at module level once, before anything else calls
// in. Globals and host functions are bound first, so src can use them. A
// Program snapshots the result, so every call starts from it.
//
//	micropython.CompileSource(ctx, src) // shorthand for
//	micropython.Compile(ctx, micropython.WithSourceScript(src))
func WithSourceScript(src string) Option {
	return optionFunc(func(o *options) {
		o.sourceScript = src
	})
}

// Globals is the set of names a program starts with, and their values.
//
//	p, err := micropython.Compile(ctx, src, micropython.WithGlobals(micropython.Globals{
//	    "NAME":   micropython.Str("service"),
//	    "LIMITS": micropython.Dict(micropython.Str("retries"), micropython.Int(3)),
//	}))
//
// The values are the built ones in value.go and nothing else, so a Go type
// with no Python equivalent is a compile error rather than an encoding one.
type Globals map[string]Value

// WithGlobals binds each name before the source runs, which is how
// configuration reaches it without being spliced into the text.
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

// WithStdout configures where the guest's print() output is written. Defaults
// to io.Discard.
//
// One writer serves the whole interpreter. For Eval and Call it is the only
// route to their output, since both return a value rather than text.
//
// Note the following:
//
//   - The caller is responsible to close any io.Writer they supply: it is not
//     closed by Instance.Close or Program.Close.
//   - w is not synchronised, so it must be safe for concurrent use if
//     interpreters share it, as a Program's pool and any Clone do. io.Pipe is;
//     bytes.Buffer is not.
func WithStdout(w io.Writer) Option {
	return optionFunc(func(o *options) {
		o.stdout = w
	})
}
