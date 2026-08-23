// Command micropython runs a Python expression, with Ctrl-C interrupting the
// guest rather than killing the process.
package main

import (
	"context"
	"fmt"
	"strings"

	micropython "github.com/gregfurman/micropython-wasi"
)

const moduleFunction = `
class module:
    def __init__(self, name, **kwargs):
        self.__name__ = name
        for key, value in kwargs.items():
            setattr(self, key, value)

def load(*args):
  # no-op
  pass

assert = module("name")

print(assert)
`

func main() {
	in, err := micropython.NewInstance(context.TODO(), micropython.WithGlobals(micropython.Globals{
		"STUFF": micropython.Dict(),
	}))
	if err != nil {
		panic(err)
	}

	out, err := in.Exec(context.TODO(), strings.ReplaceAll(string(moduleFunction), "assert", "_assert"))
	if err != nil {
		panic(err)
	}

	fmt.Printf("out: %v\n", out)
}
