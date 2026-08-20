package api

import (
	"fmt"

	"github.com/gregfurman/micropython-wasi/internal/exec"
)

func Define[In, Out any](in *exec.Instance, name, src string) (func(In) (Out, error), error) {
	fn, err := in.Define(name, src)
	if err != nil {
		return nil, err
	}
	return toFunc[In, Out](fn), nil
}

func Bind[In, Out any](in *exec.Instance, name string) (func(In) (Out, error), error) {
	fn, err := in.Func(name)
	if err != nil {
		return nil, err
	}
	return toFunc[In, Out](fn), nil
}

func BindVar[Out any](in *exec.Instance, name string) (func(...any) (Out, error), error) {
	fn, err := in.Func(name)
	if err != nil {
		return nil, err
	}
	return func(args ...any) (Out, error) {
		var zero Out

		result, err := fn.Call(args...)
		if err != nil {
			return zero, err
		}

		out, err := exec.Coerce[Out](result)
		if err != nil {
			return zero, fmt.Errorf("%s: %w", fn.Name(), err)
		}

		return out, nil
	}, nil
}

func toFunc[In, Out any](fn *exec.Func) func(In) (Out, error) {
	return func(in In) (Out, error) {
		var zero Out

		result, err := fn.Call(in)
		if err != nil {
			return zero, err
		}

		out, err := exec.Coerce[Out](result)
		if err != nil {
			return zero, fmt.Errorf("%s: %w", fn.Name(), err)
		}

		return out, nil
	}
}
