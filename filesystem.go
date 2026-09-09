package micropython

import (
	"io/fs"

	"github.com/gregfurman/micropython-go/internal/host/vfs"
)

// ReadOnly hides filesystem's write capabilities from [WithFS]. Nil stays nil.
func ReadOnly(filesystem fs.FS) fs.FS {
	if filesystem == nil {
		return nil
	}
	return readOnlyFS{filesystem}
}

type readOnlyFS struct{ filesystem fs.FS }

func (r readOnlyFS) Open(name string) (fs.File, error) { return r.filesystem.Open(name) }

// OpenFileFS optionally enables writable open calls for WithFS.
type OpenFileFS = vfs.OpenFileFS

// MkdirFS optionally enables creation of a single directory for WithFS.
type MkdirFS = vfs.MkdirFS

// UnlinkFS optionally enables removal of a file or symlink for WithFS.
// It must not follow the final symlink and must atomically reject directories.
// Go's os.Remove does not satisfy this contract.
type UnlinkFS = vfs.UnlinkFS

// RmdirFS optionally enables removal of an empty directory for WithFS.
// It must atomically reject files and symlinks, and must not remove recursively.
type RmdirFS = vfs.RmdirFS

// RenameFS optionally enables renaming within the mounted filesystem for WithFS.
type RenameFS = vfs.RenameFS
