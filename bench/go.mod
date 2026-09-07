module github.com/gregfurman/micropython-go/benchmarks

go 1.27.0

require (
	github.com/go-python/gpython v0.2.0
	github.com/goccy/go-python v0.3.0
	github.com/gregfurman/micropython-go v0.0.0-00010101000000-000000000000
)

require (
	github.com/aclements/go-moremath v0.0.0-20210112150236-f10218a38794 // indirect
	github.com/goccy/pythonwasm2go v0.3.0 // indirect
	golang.org/x/exp v0.0.0-20260824195058-e88cd73687aa // indirect
	golang.org/x/perf v0.0.0-20260825160852-19be9d8e6c70 // indirect
)

replace github.com/gregfurman/micropython-go => ..

tool golang.org/x/perf/cmd/benchstat
