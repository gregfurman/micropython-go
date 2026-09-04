package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/gregfurman/micropython-go"
)

func main() {
	ctx := context.TODO()

	numbers := []int64{5, 3, 7, 1, 4} // sum = 20

	instance, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Panicf("failed to create new micropython.Instance: %v", err)
	}

	// One argument, a list: sum([5, 3, 7, 1, 4]).
	result, err := instance.Call(ctx, "sum", numbers)
	if err != nil {
		log.Panicf("calling builtin sum failed %v", err)
	}

	sum, err := result.AsInt()
	if err != nil {
		log.Panicf("sum did not return an int %v", err)
	}

	fmt.Printf("sum(%v) = %d\n", numbers, sum)

	// res, err := instance.Call(ctx, "map", micropython.Of())
	// if err != nil {
	// 	log.Panicf("calling builtin sum failed %v", err)
	// }

	// out, err := instance.Get(ctx, "ssum")
	// if err != nil {
	// 	log.Panicf("no attribute named ssum %v", err)
	// }
	// fmt.Printf("out: %v\n", out)

	out, err := instance.Get(ctx, "sum")
	if err != nil {
		log.Panicf("no global named sum %v", err)
	}
	fmt.Printf("out: %v\n", out)

	fn, err := instance.AsCallable(out)
	if err != nil {
		log.Panicf("sum is not callable %v", err)
	}

	// got, err := instance.Eval(ctx, "lambda x: x * 2")
	// double, err := instance.AsCallable(got)
	// out, err = double(ctx, 21) // 42
	// fmt.Printf("out: %v\n", out)

	res, err := fn.Call(ctx, numbers)
	if err != nil {
		log.Panicf("calling sum through its handle failed %v", err)
	}
	fmt.Printf("sum(%v) = %v\n", numbers, res)

	err = instance.DefineFunction(ctx, "argmax", argmax)
	if err != nil {
		log.Panicf("defining custom 'argmax' from host failed %v", err)
	}

	result, err = instance.Call(ctx, "argmax", numbers)
	if err != nil {
		log.Panicf("calling host defined 'argmax' failed %v", err)
	}

	maxInd, err := result.AsInt()
	if err != nil {
		log.Panicf("argmax did not return an int %v", err)
	}

	fmt.Printf("argmax(%v) = %d\n", numbers, maxInd)

	got, err := doMap(ctx, instance)
	if err != nil {
		log.Panicf("doMap did not return properly %v", err)
	}

	fmt.Printf("got: %v\n", got)

	// double, err := instance.AsCallable(got)
	// out, err = double(ctx, 21) // 42
	// fmt.Printf("out: %v\n", out)

}

func argmax(_ context.Context, args []micropython.Value) (micropython.Value, error) {
	if len(args) != 1 {
		return micropython.Value{}, errors.New("argmax: want one sequence")
	}

	xs, err := args[0].AsList()
	if err != nil || len(xs) == 0 {
		return micropython.Value{}, errors.New("argmax: want a non-empty sequence")
	}

	maxVal, err := xs[0].AsInt()
	if err != nil {
		return micropython.Value{}, errors.New("argmax: sequence items must be ints")
	}
	maxIdx := 0
	for i := 1; i < len(xs); i++ {
		v, err := xs[i].AsInt()
		if err != nil {
			return micropython.Value{}, errors.New("argmax: sequence items must be ints")
		}
		if v > maxVal {
			maxVal, maxIdx = v, i
		}
	}

	return micropython.Int(int64(maxIdx)), nil
}

func doMap(ctx context.Context, in *micropython.Instance) (any, error) {
	got, err := in.Eval(ctx, "lambda x: x * 2")
	if err != nil {
		return nil, err
	}

	doubler, err := in.AsCallable(got)
	if err != nil {
		return nil, err
	}

	mapped, err := in.Call(ctx, "map", doubler, []int{1, 2, 3})
	if err != nil {
		return nil, err
	}

	return mapped.AsList()
}
