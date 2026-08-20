package api

import (
	"fmt"

	"github.com/gregfurman/micropython-wasi/internal/value"
)

// Define runs src, which is expected to define name, and returns a handle to
// it. This is the one-time setup for the call-many pattern:
//
//	fn, err := in.Define("score", `
//	def score(row):
//	    return row["a"] * 2 + row["b"]
//	`)
//	for _, row := range rows {
//	    v, err := fn.Call(row)
//	}
func (i *Instance) Define[In, Out any](name, src string) (func(In) (Out, error), error) {
	fn, err := i.define(name, src)
	if err != nil {
		return nil, err
	}
	return toFunc[In, Out](fn), nil
}

func (i *Instance) Bind[In, Out any](name string) (func(In) (Out, error), error) {
	fn, err := i.Func(name)
	if err != nil {
		return nil, err
	}
	return toFunc[In, Out](fn), nil
}

func (i *Instance) BindVar[Out any](name string) (func(...any) (Out, error), error) {
	fn, err := i.Func(name)
	if err != nil {
		return nil, err
	}
	return func(args ...any) (Out, error) {
		result, err := fn.Call(args...)
		return coerced[Out](fn, result, err)
	}, nil
}

func toFunc[In, Out any](fn *Func) func(In) (Out, error) {
	return func(in In) (Out, error) {
		result, err := fn.Call(in)
		return coerced[Out](fn, result, err)
	}
}

// coerced narrows a call's untyped result to Out, naming the function if it
// does not fit.
func coerced[Out any](fn *Func, result any, err error) (Out, error) {
	var zero Out
	if err != nil {
		return zero, err
	}

	out, err := value.Coerce[Out](result)
	if err != nil {
		return zero, fmt.Errorf("%s: %w", fn.Name(), err)
	}
	return out, nil
}
