package vfs

import (
	"encoding/binary"
	"io/fs"
	"math"
	"strings"

	"github.com/gregfurman/micropython-go/internal/host/abi"
)

const maxPathLength = 4096

// name reads a guest path and returns it in io/fs form: slash separated,
// unrooted and valid. Do not clean internal dots or trailing slashes: that
// could turn a failing directory lookup into a successful file mutation.
func (f *Filesystem) name(ptr, size int32) (string, int32) {
	if f.closed {
		return "", -abi.EBADF
	}

	b, status := f.view(ptr, size)
	if status != 0 {
		return "", status
	}

	if f.fs == nil {
		return "", -abi.EACCES
	}

	if len(b) > maxPathLength {
		return "", -abi.ENAMETOOLONG
	}

	name := string(b)
	if strings.ContainsAny(name, "\x00\\") {
		return "", -abi.EINVAL
	}

	for part := range strings.SplitSeq(name, "/") {
		if part == ".." {
			return "", -abi.EACCES
		}
	}

	name = strings.TrimLeft(name, "/")
	for strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}

	if name == "" {
		name = "."
	}

	if !fs.ValidPath(name) {
		return "", -abi.EINVAL
	}

	return name, 0
}

// target is name for operations that modify the entry they name. The mount
// root is a valid path to read but never a valid thing to create or destroy.
func (f *Filesystem) target(ptr, size int32) (string, int32) {
	name, status := f.name(ptr, size)
	if status != 0 {
		return "", status
	}

	if name == "." {
		return "", -abi.EACCES
	}

	return name, 0
}

func mode(m fs.FileMode) uint32 {
	if m.IsDir() {
		return 0040000
	}

	return 0100000
}

func (f *Filesystem) Xhost_fs_stat(ptr, size, outPtr int32) int32 {
	name, status := f.name(ptr, size)
	if status != 0 {
		return status
	}

	out, status := f.view(outPtr, 12)
	if status != 0 {
		return status
	}

	info, err := fs.Stat(f.fs, name)
	if err != nil {
		return errnoOf(err)
	}

	mtime := info.ModTime().Unix()
	if info.ModTime().IsZero() {
		mtime = 0
	}

	if info.Size() < 0 || info.Size() > math.MaxUint32 || mtime < 0 || mtime > math.MaxUint32 {
		return -abi.EFBIG
	}

	binary.LittleEndian.PutUint32(out, mode(info.Mode()))
	binary.LittleEndian.PutUint32(out[4:], uint32(info.Size()))
	binary.LittleEndian.PutUint32(out[8:], uint32(mtime))

	return 0
}

func (f *Filesystem) Xhost_fs_mkdir(ptr, size int32) int32 {
	name, status := f.target(ptr, size)
	if status != 0 {
		return status
	}

	backend, ok := f.fs.(MkdirFS)
	if !ok {
		return -abi.EROFS
	}

	return errnoOf(backend.Mkdir(name, 0777))
}

func (f *Filesystem) Xhost_fs_remove(ptr, size int32) int32 {
	name, status := f.target(ptr, size)
	if status != 0 {
		return status
	}

	backend, ok := f.fs.(UnlinkFS)
	if !ok {
		return -abi.EROFS
	}

	return errnoOf(backend.Unlink(name))
}

func (f *Filesystem) Xhost_fs_rmdir(ptr, size int32) int32 {
	name, status := f.target(ptr, size)
	if status != 0 {
		return status
	}

	backend, ok := f.fs.(RmdirFS)
	if !ok {
		return -abi.EROFS
	}

	return errnoOf(backend.Rmdir(name))
}

func (f *Filesystem) Xhost_fs_rename(oldPtr, oldSize, newPtr, newSize int32) int32 {
	oldName, status := f.target(oldPtr, oldSize)
	if status != 0 {
		return status
	}

	newName, status := f.target(newPtr, newSize)
	if status != 0 {
		return status
	}

	backend, ok := f.fs.(RenameFS)
	if !ok {
		return -abi.EROFS
	}

	return errnoOf(backend.Rename(oldName, newName))
}
