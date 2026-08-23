package host

import (
	"fmt"
	"strings"

	"github.com/gregfurman/micropython-wasi/internal/value"
)

// Binding a Go function so Python can call it.
//
// Every host function has the same signature and unpacks its own arguments,
// which is what starlark-go does with UnpackArgs. It looks like the least
// convenient of the options until the alternatives are written out: reflection
// discovers the signature at run time and calls through reflect.Value, moving
// every check past the compiler; generics need one constructor per arity,
// because Go has no variadic type parameter list, and still cannot express
// keyword arguments.
//
// Unpack is neither. It is a loop over name and pointer pairs, and each pointer
// is assigned through a type switch on its own type -- so the destination types
// are checked where they are written, arity and names come from the same list,
// and keyword arguments are the same code as positional ones.

// Call is one invocation of a host function, before its arguments have been
// taken apart.
type Call struct {
	name   string
	args   []any
	kwargs map[string]any
}

// Name is the function's name, as the guest called it.
func (c *Call) Name() string { return c.name }

// Args and Kwargs are the arguments as they arrived, for a function that would
// rather read them itself.
func (c *Call) Args() []any            { return c.args }
func (c *Call) Kwargs() map[string]any { return c.kwargs }

// Unpack assigns the arguments to the destinations named by pairs, which
// alternate a parameter name and a pointer to the variable it belongs in:
//
//	var x, y float64
//	if err := c.Unpack("x", &x, "y", &y); err != nil {
//	    return nil, err
//	}
//
// Positional arguments fill the pairs in order, and keyword arguments fill them
// by name. A name ending in "?" is optional: its destination is left as it was
// when the argument is missing, which is how a default is expressed.
//
// Anything that goes wrong -- too many arguments, an unknown keyword, one
// given twice, a value of the wrong type -- is reported in the guest's terms
// and becomes an exception at the call site.
func (c *Call) Unpack(pairs ...any) error {
	if len(pairs)%2 != 0 {
		return fmt.Errorf("%s(): Unpack needs a name and a pointer for each parameter", c.name)
	}
	n := len(pairs) / 2

	if len(c.args) > n {
		return fmt.Errorf("%s() takes at most %d arguments (%d given)", c.name, n, len(c.args))
	}

	filled := make([]bool, n)
	for i, arg := range c.args {
		if err := c.assign(pairs[2*i], pairs[2*i+1], arg); err != nil {
			return err
		}
		filled[i] = true
	}

	for key, arg := range c.kwargs {
		i := -1
		for j := 0; j < n; j++ {
			if name, _ := paramName(pairs[2*j]); name == key {
				i = j
				break
			}
		}
		if i < 0 {
			return fmt.Errorf("%s() got an unexpected keyword argument %q", c.name, key)
		}
		if filled[i] {
			return fmt.Errorf("%s() got multiple values for argument %q", c.name, key)
		}
		if err := c.assign(pairs[2*i], pairs[2*i+1], arg); err != nil {
			return err
		}
		filled[i] = true
	}

	for i := 0; i < n; i++ {
		name, optional := paramName(pairs[2*i])
		if !filled[i] && !optional {
			return fmt.Errorf("%s() missing argument %q", c.name, name)
		}
	}
	return nil
}

// unpack is value.Unpack plus the types this package owns. It is split that
// way because internal/value cannot see an Object without importing this
// package back.
func unpack(src, dst any) error {
	if p, ok := dst.(*Object); ok {
		v, ok := src.(Object)
		if !ok {
			return fmt.Errorf("cannot use %s as *Object", value.Name(src))
		}
		*p = v
		return nil
	}
	return value.Unpack(src, dst)
}

func (c *Call) assign(nameArg, dst, arg any) error {
	name, _ := paramName(nameArg)
	if err := unpack(arg, dst); err != nil {
		return fmt.Errorf("%s() argument %q: %w", c.name, name, err)
	}
	return nil
}

// paramName reads one name from a pairs list, and whether it is optional.
func paramName(v any) (string, bool) {
	name, _ := v.(string)
	if strings.HasSuffix(name, "?") {
		return strings.TrimSuffix(name, "?"), true
	}
	return name, false
}

// Bind wraps a Go function so the guest can call it.
func Bind(name string, fn func(*Call) (any, error)) *Func {
	return &Func{Name: name, call: fn}
}
