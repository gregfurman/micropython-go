package vfs

import (
	"errors"
	"io"
	"io/fs"
	"syscall"

	"github.com/gregfurman/micropython-go/internal/host/abi"
)

var hostErrnos = []struct {
	err  error
	code int32
}{
	{syscall.EBADF, abi.EBADF},
	{syscall.ENOTDIR, abi.ENOTDIR},
	{syscall.EISDIR, abi.EISDIR},
	{syscall.EFBIG, abi.EFBIG},
	{syscall.EOVERFLOW, abi.EFBIG},
	{syscall.ENOSPC, abi.ENOSPC},
	{syscall.ESPIPE, abi.ESPIPE},
	{syscall.EROFS, abi.EROFS},
	{syscall.EXDEV, abi.EXDEV},
	{syscall.EBUSY, abi.EBUSY},
	{syscall.ENAMETOOLONG, abi.ENAMETOOLONG},
	{syscall.ENOTEMPTY, abi.ENOTEMPTY},
	{syscall.ELOOP, abi.ELOOP},
	{syscall.EMFILE, abi.EMFILE},
	{syscall.EINTR, abi.EINTR},
	{syscall.EOPNOTSUPP, abi.EOPNOTSUPP},
	// Generic fs errors match some OS errors broadly (ENOTEMPTY matches Exist).
	// Preserve the more specific errno above before applying these fallbacks.
	{fs.ErrNotExist, abi.ENOENT},
	{fs.ErrPermission, abi.EACCES},
	{fs.ErrExist, abi.EEXIST},
	{fs.ErrInvalid, abi.EINVAL},
	{fs.ErrClosed, abi.EBADF},
}

func errnoOf(err error) int32 {
	if err == nil {
		return 0
	}
	for _, entry := range hostErrnos {
		if errors.Is(err, entry.err) {
			return -entry.code
		}
	}
	return -abi.EIO
}

func readResult(n, capacity int, err error) int32 {
	if n < 0 || n > capacity {
		return -abi.EIO
	}
	if n > 0 {
		return int32(n)
	}
	if errors.Is(err, io.EOF) {
		return 0
	}
	if err == nil && capacity > 0 {
		return -abi.EIO
	}
	return errnoOf(err)
}
