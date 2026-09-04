package main

import (
	"context"
	"log"

	"github.com/gregfurman/micropython-go"
)

func main() {
	ctx := context.TODO()
	in, err := micropython.NewInstance(ctx)
	if err != nil {
		log.Panicf("failed to create new micropython.Instance: %v", err)
	}

	val, err := in.Eval(ctx, `(x ** 2 for x in range(1, 4))`)
	if err != nil {
		log.Panicf("failed to evaluate generator expression: %v", err)
	}

	if !val.IsIterator() {
		log.Panicf("evaluated value should be an iterator: %v", err)
	}

	gen, err := in.AsIterator(val)
	if err != nil {
		log.Panicf("failed to bind iterator to instance: %v", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	cancel()
	for v, err := range gen.Iter(ctx) {
		if err != nil {
			log.Panicf("failed to retrieve next iteration with error: %s", err)
			break
		}
		log.Printf("v.Export(): %v\n", v.Export())
	}
}
