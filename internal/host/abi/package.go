//go:generate sh -c "go tool cgo -godefs -- -I../../../build -m32 abi_cgo.go > abi.go"
//go:generate sh -c "go tool cgo -godefs -- -I../../../build -I../../../micropython -I../../../micropython/ports/embed -I../../../micropython/ports/embed/port -m32 errno_cgo.go > errno.go"
//go:generate go fmt ./...
package abi
