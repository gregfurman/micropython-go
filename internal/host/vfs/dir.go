package vfs

import (
	"encoding/binary"
	"io/fs"
	"strings"
	"syscall"

	"github.com/gregfurman/micropython-go/internal/host/abi"
)

func (f *Filesystem) Xhost_fs_opendir(ptr, size int32) int32 {
	name, status := f.name(ptr, size)
	if status != 0 {
		return status
	}
	if status := f.capacity(); status != 0 {
		return status
	}
	opened, err := f.fs.Open(name)
	if err != nil {
		if opened != nil {
			_ = opened.Close()
		}
		return errnoOf(err)
	}
	if opened == nil {
		return -abi.EIO
	}
	info, err := opened.Stat()
	if err == nil && !info.IsDir() {
		err = syscall.ENOTDIR
	}
	dir, ok := opened.(fs.ReadDirFile)
	if err == nil && !ok {
		err = syscall.EOPNOTSUPP
	}
	if err != nil {
		_ = opened.Close()
		return errnoOf(err)
	}
	return f.add(&openDir{ReadDirFile: dir})
}

func (f *Filesystem) Xhost_fs_readdir(fd, ptr, size, modePtr int32) int32 {
	dir, status := f.dir(fd)
	if status != 0 {
		return status
	}
	out, status := f.view(ptr, size)
	if status != 0 {
		return status
	}
	modeOut, status := f.view(modePtr, 4)
	if status != 0 {
		return status
	}
	if dir.entry == nil {
		if dir.err != nil {
			return readResult(0, 0, dir.err)
		}
		entries, err := dir.ReadDir(1)
		if len(entries) > 1 {
			return -abi.EIO
		}
		dir.err = err
		if len(entries) == 0 {
			if err == nil {
				return -abi.EIO
			}
			return readResult(0, 0, err)
		}
		dir.entry = entries[0]
	}
	name := dir.entry.Name()
	if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "/\\\x00") {
		return -abi.EIO
	}
	if len(name) > len(out) {
		return -abi.ENAMETOOLONG
	}
	copy(out, name)
	binary.LittleEndian.PutUint32(modeOut, mode(dir.entry.Type()))
	dir.entry = nil
	return int32(len(name))
}

func (f *Filesystem) Xhost_fs_closedir(fd int32) int32 {
	if _, status := f.dir(fd); status != 0 {
		return status
	}
	return f.Xhost_fs_close(fd)
}
