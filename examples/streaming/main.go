package main

import (
	"context"
	"fmt"
	"log"

	"github.com/gregfurman/micropython-go"
)

func main() {
	ctx := context.TODO()

	instance, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Panicf("failed to create new micropython.Instance: %v", err)
	}

	type row struct {
		Name string
		Age  int64
	}

	inChan := make(chan row, 1)
	defer close(inChan)

	instance.DefineFunction(ctx, "emit", func(ctx context.Context, args []micropython.Value) (micropython.Value, error) {
		select {
		case val, ok := <-inChan:
			if ok {
				return micropython.Of(val), nil
			}
		case <-ctx.Done():
			return micropython.None(), ctx.Err()
		}

		return micropython.None(), nil
	})

	go func() {
		for _, r := range [...]row{
			{Name: "Greg", Age: 27},
			{Name: "Alex", Age: 30},
			{Name: "Mia", Age: 26},
		} {
			inChan <- r
		}
	}()

	for range 3 {
		val, err := instance.Eval(ctx, "emit()")
		if err != nil {
			log.Panicf("failed to eval emit(): %v", err)
		}

		fmt.Printf("val: %v\n", val.Export())
	}

}
