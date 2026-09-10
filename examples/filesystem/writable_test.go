package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"github.com/gregfurman/micropython-go"
)

// Holding the root, rather than embedding it, grants only these two methods.
type writableFS struct{ root *os.Root }

func (f writableFS) Open(name string) (fs.File, error) {
	return f.root.Open(name)
}

func (f writableFS) OpenFile(name string, flags int, perm fs.FileMode) (fs.File, error) {
	return f.root.OpenFile(name, flags, perm)
}

var _ fs.FS = writableFS{}
var _ micropython.OpenFileFS = writableFS{}

func Example_writableFilesystem() {
	dir, err := os.MkdirTemp("", "micropython-filesystem-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	root, err := os.OpenRoot(dir)
	if err != nil {
		panic(err)
	}
	defer root.Close()

	ctx := context.Background()
	in, err := micropython.NewInstance(ctx, micropython.WithFS(writableFS{root}))
	if err != nil {
		panic(err)
	}
	defer in.Close()
	if err := in.Exec(ctx, "with open('result.txt', 'w') as f:\n    f.write('hello')"); err != nil {
		panic(err)
	}
	data, err := root.ReadFile("result.txt")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data))
	// Output: hello
}
