// Package vfs implements the guest's fs imports over an io/fs filesystem.
package vfs

import (
	"errors"
	"io/fs"
	"math"

	"github.com/gregfurman/micropython-go/internal/host/abi"
	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/micropython"
)

var _ micropython.Xfs = (*Filesystem)(nil)

// A backend implements fs.FS and, to gain the matching operation, any of the
// interfaces below. A backend that omits one gets EROFS for that operation.
// Assert against them where the backend is defined to catch a mismatched
// signature at compile time rather than as a refusal from Python.

type OpenFileFS interface {
	OpenFile(name string, flag int, perm fs.FileMode) (fs.File, error)
}
type MkdirFS interface {
	Mkdir(name string, perm fs.FileMode) error
}

// UnlinkFS removes a file or symlink without following the final symlink.
// It must atomically reject directories; os.Remove does not meet this contract.
type UnlinkFS interface {
	Unlink(name string) error
}

// RmdirFS removes an empty directory, atomically rejecting files and symlinks.
type RmdirFS interface {
	Rmdir(name string) error
}

type RenameFS interface {
	Rename(oldName, newName string) error
}

// Filesystem owns open handles, not the mounted backend. Serialize its methods
// with guest execution. Backends must not retain I/O buffers or reenter the
// interpreter, and must enforce their own symlink confinement. Operations are
// synchronous; fs.FS does not provide context cancellation.
type Filesystem struct {
	mem     *memory.Memory
	fs      fs.FS
	handles map[int32]fs.File
	next    int64
	closed  bool
}

// A handle is either an openFile or an openDir, never both. Keeping the two
// apart is what lets every operation skip re-deriving which one it was given.

type openFile struct {
	fs.File
	flags   int32
	readErr error
}

type openDir struct {
	fs.ReadDirFile
	entry fs.DirEntry
	err   error
}

const maxHandles = 64

// New mounts backend at the guest root. Nil denies filesystem access.
func New(mem *memory.Memory, mounted fs.FS) *Filesystem {
	return &Filesystem{
		mem:     mem,
		fs:      mounted,
		handles: make(map[int32]fs.File),
	}
}

func (f *Filesystem) HasOpenFiles() bool {
	return len(f.handles) != 0
}

// Close releases all handles but does not close the caller-owned backend.
func (f *Filesystem) Close() error {
	f.closed = true

	var err error

	for fd, open := range f.handles {
		delete(f.handles, fd)

		err = errors.Join(err, open.Close())
	}

	return err
}

// Reset releases handles before a rewind. Descriptor numbers are never reused.
func (f *Filesystem) Reset(mounted fs.FS) error {
	err := f.Close()
	f.fs, f.closed = mounted, false

	return err
}

func (f *Filesystem) view(ptr, size int32) ([]byte, int32) {
	if f.mem == nil {
		return nil, -abi.EFAULT
	}

	b, err := f.mem.View(ptr, size)
	if err != nil {
		return nil, -abi.EFAULT
	}

	return b, 0
}

func (f *Filesystem) get(fd int32) (fs.File, int32) {
	open := f.handles[fd]
	if f.closed || open == nil {
		return nil, -abi.EBADF
	}

	return open, 0
}

func (f *Filesystem) file(fd int32) (*openFile, int32) {
	open, status := f.get(fd)
	if status != 0 {
		return nil, status
	}

	file, ok := open.(*openFile)
	if !ok {
		return nil, -abi.EBADF
	}

	return file, 0
}

func (f *Filesystem) dir(fd int32) (*openDir, int32) {
	open, status := f.get(fd)
	if status != 0 {
		return nil, status
	}

	dir, ok := open.(*openDir)
	if !ok {
		return nil, -abi.ENOTDIR
	}

	return dir, 0
}

func (f *Filesystem) capacity() int32 {
	if len(f.handles) >= maxHandles || f.next > math.MaxInt32 {
		return -abi.EMFILE
	}

	return 0
}

func (f *Filesystem) add(open fs.File) int32 {
	fd := int32(f.next)
	f.next++
	f.handles[fd] = open

	return fd
}

func (f *Filesystem) Xhost_fs_close(fd int32) int32 {
	open, status := f.get(fd)
	if status != 0 {
		return status
	}

	delete(f.handles, fd)

	return errnoOf(open.Close())
}

func (f *Filesystem) FS() fs.FS {
	return f.fs
}
