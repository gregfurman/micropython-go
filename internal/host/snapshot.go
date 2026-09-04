package host

import (
	"io"
)

type Snapshot struct {
	memory   []byte
	stack    int32
	scratch  int32
	walk     []int32
	registry map[int32]HostFunc
	counter  int32
	stdout   io.Writer
}

// Stdout reports the sink interpreters restored from this snapshot will use.
func (s *Snapshot) Stdout() io.Writer { return s.stdout }

// Restore builds a fresh interpreter from the image. It differs from
// Module.Restore only in allocating the module to restore into, so the rewind
// itself lives in one place.
func (s *Snapshot) Restore() (*Module, error) {
	i := newModule(s.stdout)
	if err := i.Restore(s); err != nil {
		return nil, err
	}
	return i, nil
}
