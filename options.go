package micropython

type options struct {
	programPoolSize int
}

type option func(o *options)

func WithPoolSize(n int) func(o *options) {
	return func(o *options) {
		o.programPoolSize = n
	}
}
