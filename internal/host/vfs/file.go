package vfs

import (
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"syscall"

	"github.com/gregfurman/micropython-go/internal/host/abi"
)

// Flags defined in build/host.h, not the host OS's open flags.
const (
	read = 1 << iota
	write
	appendMode
	create
	truncate
)

func (f *Filesystem) Xhost_fs_open(ptr, size, flags int32) int32 {
	name, status := f.name(ptr, size)
	if status != 0 {
		return status
	}

	if !validOpenFlags(flags) {
		return -abi.EINVAL
	}

	if status := f.capacity(); status != 0 {
		return status
	}

	opened, err := f.openWith(name, flags)
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
	if err == nil {
		switch {
		case info.IsDir():
			err = syscall.EISDIR
		case !info.Mode().IsRegular():
			err = syscall.EOPNOTSUPP
		}
	}

	if err == nil && flags&appendMode != 0 {
		if seeker, ok := opened.(io.Seeker); ok {
			_, err = seeker.Seek(0, io.SeekEnd)
		}
	}

	if err != nil {
		_ = opened.Close()
		return errnoOf(err)
	}

	return f.add(&openFile{File: opened, flags: flags})
}

func validOpenFlags(flags int32) bool {
	if flags & ^int32(read|write|appendMode|create|truncate) != 0 || flags&(read|write) == 0 {
		return false
	}

	if flags&(appendMode|create|truncate) != 0 && flags&write == 0 {
		return false
	}

	return flags&(appendMode|truncate) != appendMode|truncate
}

func (f *Filesystem) openWith(name string, flags int32) (fs.File, error) {
	if flags&write == 0 {
		return f.fs.Open(name)
	}

	backend, ok := f.fs.(OpenFileFS)
	if !ok {
		return nil, syscall.EROFS
	}

	mode := os.O_WRONLY
	if flags&read != 0 {
		mode = os.O_RDWR
	}

	if flags&appendMode != 0 {
		mode |= os.O_APPEND
	}

	if flags&create != 0 {
		mode |= os.O_CREATE
	}

	if flags&truncate != 0 {
		mode |= os.O_TRUNC
	}

	return backend.OpenFile(name, mode, 0666)
}

func (f *Filesystem) Xhost_fs_read(fd, ptr, size int32) int32 {
	file, status := f.file(fd)
	if status != 0 {
		return status
	}

	if file.flags&read == 0 {
		return -abi.EBADF
	}

	b, status := f.view(ptr, size)
	if status != 0 {
		return status
	}

	if len(b) == 0 {
		return 0
	}

	if file.readErr != nil {
		err := file.readErr
		file.readErr = nil

		return readResult(0, len(b), err)
	}

	n, err := file.Read(b)
	if n > 0 && n <= len(b) && !errors.Is(err, io.EOF) {
		file.readErr = err
	}

	return readResult(n, len(b), err)
}

func (f *Filesystem) Xhost_fs_write(fd, ptr, size int32) int32 {
	file, status := f.file(fd)
	if status != 0 {
		return status
	}

	if file.flags&write == 0 {
		return -abi.EBADF
	}

	b, status := f.view(ptr, size)
	if status != 0 {
		return status
	}

	w, ok := file.File.(io.Writer)
	if !ok {
		return -abi.EOPNOTSUPP
	}

	n, err := w.Write(b)
	if n < 0 || n > len(b) {
		return -abi.EIO
	}

	if n > 0 {
		file.readErr = nil
		return int32(n)
	}

	if err != nil {
		return errnoOf(err)
	}

	if len(b) != 0 {
		return -abi.EIO
	}

	return 0
}

func (f *Filesystem) Xhost_fs_seek(fd, offset, whence, outPtr int32) int32 {
	file, status := f.file(fd)
	if status != 0 {
		return status
	}

	out, status := f.view(outPtr, 4)
	if status != 0 {
		return status
	}

	if whence < io.SeekStart || whence > io.SeekEnd {
		return -abi.EINVAL
	}

	s, ok := file.File.(io.Seeker)
	if !ok {
		return -abi.ESPIPE
	}
	// Resolve relative offsets before moving the cursor, so a rejected seek
	// leaves both the position and any pending read error unchanged.
	var (
		base int64
		err  error
	)

	switch whence {
	case io.SeekCurrent:
		base, err = s.Seek(0, io.SeekCurrent)
	case io.SeekEnd:
		var info fs.FileInfo

		info, err = file.Stat()
		if err == nil {
			base = info.Size()
		}
	}

	if err != nil {
		return errnoOf(err)
	}
	// Check before adding: a backend can report an arbitrarily large size.
	if base < -int64(offset) {
		return -abi.EINVAL
	}

	if base > math.MaxInt32-int64(offset) {
		return -abi.EFBIG
	}

	position, err := s.Seek(base+int64(offset), io.SeekStart)
	if err != nil {
		return errnoOf(err)
	}

	if position != base+int64(offset) {
		return -abi.EIO
	}

	file.readErr = nil

	binary.LittleEndian.PutUint32(out, uint32(position))

	return 0
}

func (f *Filesystem) Xhost_fs_flush(fd int32) int32 {
	file, status := f.file(fd)
	if status != 0 {
		return status
	}

	if file.flags&write == 0 {
		return 0
	}

	if flush, ok := file.File.(interface{ Flush() error }); ok {
		if err := flush.Flush(); err != nil {
			return errnoOf(err)
		}
	}

	if sync, ok := file.File.(interface{ Sync() error }); ok {
		return errnoOf(sync.Sync())
	}

	return 0
}
