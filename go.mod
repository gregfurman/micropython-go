module github.com/gregfurman/micropython-go

go 1.27.0

tool (
	github.com/ncruces/wasm2go
	github.com/ncruces/wasm2go/libc-gen
)

require (
	github.com/ncruces/wasm2go v0.4.11 // indirect
	golang.org/x/tools v0.49.0 // indirect
)
