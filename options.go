package micropython

import (
	"sort"
	"strings"

	"github.com/gregfurman/micropython-wasi/internal/host"
)

type options struct {
	programPoolSize int

	funcs   *host.Registry
	globals []global

	// The first error a WithX option ran into. Options cannot fail on their
	// own, so it is carried here and reported by whoever applied them.
	err error
}

type global struct {
	name  string
	value any
}

type option func(o *options)

func WithPoolSize(n int) option {
	return func(o *options) {
		o.programPoolSize = n
	}
}

// Declare is the set of names a program starts with -- host functions and
// plain values together, the same shape as the predeclared StringDict
// starlark-go passes to ExecFile.
//
//	p, err := micropython.Compile(ctx, src, micropython.Declare{
//	    "pow": micropython.Fn(func(c *micropython.Call) (any, error) {
//	        var x, y float64
//	        if err := c.Unpack("x", &x, "y", &y); err != nil {
//	            return nil, err
//	        }
//	        return math.Pow(x, y), nil
//	    }),
//	    "MAX_RETRIES": 3,
//	}.Option())
type Declare map[string]any

// Option makes the declarations an option Compile and NewInstance accept.
func (d Declare) Option() option {
	return func(o *options) { d.apply(o) }
}

func (d Declare) apply(o *options) {
	// Sorted, so a function's id depends on its name rather than on map
	// iteration order. The ids live in the snapshot, and one that differed run
	// to run would be a poor thing to cache or compare.
	names := make([]string, 0, len(d))
	for name := range d {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		fn, ok := d[name].(*host.Func)
		if !ok {
			o.globals = append(o.globals, global{name, d[name]})
			continue
		}

		if fn.Name == "" {
			fn.Name = name
		}
		if o.funcs == nil {
			o.funcs = &host.Registry{}
		}
		o.funcs.Add(fn)
	}
}

// Fn wraps a Go function for a Declare, taking its name from the key it is
// declared under. A trailing string becomes its __doc__.
//
// The function unpacks its own arguments, which is what lets one signature
// serve every arity and keyword arguments alike; see Call.Unpack.
//
// It runs on whichever goroutine made the call, and a Program with more than
// one interpreter can have several in flight at once, so it has to be safe for
// concurrent use.
func Fn(fn func(c *Call) (any, error), doc ...string) *host.Func {
	f := host.Bind("", fn)
	f.Doc = strings.Join(doc, "\n")
	return f
}

// WithFunc declares a single host function, for when a whole Declare is more
// than the occasion needs.
func WithFunc(name string, fn func(c *Call) (any, error), doc ...string) option {
	return Declare{name: Fn(fn, doc...)}.Option()
}

// WithGlobal binds a value to a Python global before the source runs, which is
// how configuration reaches it without being spliced into the text.
//
//	micropython.WithGlobal("LIMITS", map[string]any{"retries": 3})
func WithGlobal(name string, value any) option {
	return func(o *options) {
		o.globals = append(o.globals, global{name, value})
	}
}

func newOptions(opts []option) (*options, error) {
	o := &options{}
	for _, apply := range opts {
		apply(o)
	}
	return o, o.err
}
