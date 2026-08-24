package micropython

type options struct {
	programPoolSize int

	globals map[string]Value
}

type option func(o *options)

func WithPoolSize(n int) option {
	return func(o *options) {
		o.programPoolSize = n
	}
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
func WithGlobals(g Globals) option {
	return func(o *options) {
		o.globals = g
	}
}

func newOptions(opts []option) *options {
	o := &options{}
	for _, apply := range opts {
		apply(o)
	}
	return o
}
