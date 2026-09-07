package main

import (
	"context"
	"fmt"

	"github.com/gregfurman/micropython-go"
)

func main() {

	in, _ := micropython.NewInstance(context.TODO())

	val, _ := in.Eval(context.TODO(), "(lambda: 1).__code__")

	fmt.Printf("val.Export(): %v\n", val.Export())

}
