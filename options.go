package micropython

import "sort"

type options struct {
	programPoolSize int

	globals []global
}

type global struct {
	name  string
	value Value
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
		// Sorted, so the globals land in the same order every time. They are
		// independent of each other, but the snapshot is a copy of memory, and
		// one that differed run to run would be a poor thing to cache or
		// compare.
		names := make([]string, 0, len(g))
		for name := range g {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			o.globals = append(o.globals, global{name, g[name]})
		}
	}
}

func newOptions(opts []option) *options {
	o := &options{}
	for _, apply := range opts {
		apply(o)
	}
	return o
}
