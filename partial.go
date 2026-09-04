package micropython

// import (
// 	"fmt"

// 	"github.com/gregfurman/micropython-go/internal/host/codec"
// )

// type IValue interface {
// 	kind() codec.Kind
// }

// type Argument interface {
// 	Name() string
// 	Kind() codec.Kind
// }

// type typedArgument[V IValue] struct {
// 	name string
// }

// func (a typedArgument[V]) Name() string {
// 	return a.name
// }

// func (a typedArgument[V]) Kind() codec.Kind {
// 	var value V
// 	return value.kind()
// }

// func Arg[V IValue](name string) Argument {
// 	return typedArgument[V]{
// 		name: name,
// 	}
// }

// type Func[R IValue] func(...IValue) (R, error)

// type callableBuilder struct{}

// func Callable() *callableBuilder {
// 	return &callableBuilder{}
// }

// func (c *callableBuilder) FromFunc[F any]() F {
// 	return *new(F)
// }

// type DynamicFunction struct {
// 	args []Argument
// }

// func (c *callableBuilder) Arg[V IValue](name string) *DynamicFunction {
// 	return (&DynamicFunction{}).Arg[V](name)
// }

// func (c *callableBuilder) Args(args ...Argument) *DynamicFunction {
// 	return (&DynamicFunction{}).Args(args...)
// }

// func (f *DynamicFunction) Arg[V IValue](name string) *DynamicFunction {
// 	return f.Args(Arg[V](name))
// }

// func (f *DynamicFunction) Args(args ...Argument) *DynamicFunction {
// 	combined := make([]Argument, 0, len(f.args)+len(args))
// 	combined = append(combined, f.args...)
// 	combined = append(combined, args...)

// 	return &DynamicFunction{
// 		args: combined,
// 	}
// }

// func (f *DynamicFunction) Arity() int {
// 	return len(f.args)
// }

// func (f *DynamicFunction) Arguments() []Argument {
// 	return append([]Argument(nil), f.args...)
// }

// func (f *DynamicFunction) Result[R IValue]() Func[R] {
// 	expected := f.Arguments()

// 	return func(values ...IValue) (R, error) {
// 		var result R

// 		if len(values) != len(expected) {
// 			return result, fmt.Errorf(
// 				"expected %d arguments, got %d",
// 				len(expected),
// 				len(values),
// 			)
// 		}

// 		for index, value := range values {
// 			argument := expected[index]

// 			if value == nil {
// 				return result, fmt.Errorf(
// 					"argument %q is nil",
// 					argument.Name(),
// 				)
// 			}

// 			want := argument.Kind()
// 			got := value.kind()

// 			if got != want {
// 				return result, fmt.Errorf(
// 					"argument %q: expected %v, got %v",
// 					argument.Name(),
// 					want,
// 					got,
// 				)
// 			}
// 		}

// 		return result, nil
// 	}
// }
