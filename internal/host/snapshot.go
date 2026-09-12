package host

import (
	"io"
	"io/fs"

	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/host/network"
)

type Snapshot struct {
	memory     []byte
	stack      int32
	arena      memory.ArenaState
	registry   map[int32]HostFunc
	counter    int32
	stdout     io.Writer
	network    network.Config
	filesystem fs.FS
	vars       map[string]string
}

// Stdout reports the sink interpreters restored from this snapshot will use.
func (s *Snapshot) Stdout() io.Writer { return s.stdout }

func (s *Snapshot) NetworkConfig() network.Config { return s.network }

func (s *Snapshot) Filesystem() fs.FS { return s.filesystem }

func (s *Snapshot) Vars() map[string]string { return s.vars }

// Restore builds a fresh interpreter from the image. It differs from
// Module.Restore only in allocating the module to restore into, so the rewind
// itself lives in one place.
func (s *Snapshot) Restore() (*Module, error) {
	i := newModule(s.stdout, s.network, s.filesystem, s.vars)
	if err := i.Restore(s); err != nil {
		return nil, err
	}

	return i, nil
}
