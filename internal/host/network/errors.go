package network

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"

	"github.com/gregfurman/micropython-go/internal/host/abi"
)

const (
	// STATUS_OK is the success sentinel. Anything positive will raise an invalid protocol error.
	STATUS_OK int32 = 0
)

// hostErrnos pairs the host's errno identities with MicroPython's numbers.
// Matching goes through errors.Is rather than arithmetic because the two
// numbering schemes disagree e.g ECONNREFUSED is 61 on darwin and 111 in the
// guest, so forwarding a syscall.Errno would report the wrong failure on any
// host that is not Linux.
//
// ENETUNREACH has no counterpart in py/mperrno.h, which skips from 113 to 114,
// so it folds into EHOSTUNREACH as the nearest thing Python can act on.
var hostErrnos = [...]struct {
	host  error
	guest int32
}{
	{syscall.EPERM, abi.EPERM},
	{syscall.ECONNREFUSED, abi.ECONNREFUSED},
	{syscall.ECONNRESET, abi.ECONNRESET},
	{syscall.ECONNABORTED, abi.ECONNABORTED},
	{syscall.EHOSTUNREACH, abi.EHOSTUNREACH},
	{syscall.ENETUNREACH, abi.EHOSTUNREACH},
	{syscall.ETIMEDOUT, abi.ETIMEDOUT},
	{syscall.EPIPE, abi.EPIPE},
	{syscall.EADDRINUSE, abi.EADDRINUSE},
	{syscall.EISCONN, abi.EISCONN},
	{syscall.ENOTCONN, abi.ENOTCONN},
	{syscall.EAGAIN, abi.EAGAIN},
	{syscall.EINPROGRESS, abi.EINPROGRESS},
	{syscall.EALREADY, abi.EALREADY},
	{syscall.EACCES, abi.EACCES},
	{syscall.EMFILE, abi.EMFILE},
	{syscall.ENFILE, abi.ENFILE},
	{syscall.ENOBUFS, abi.ENOBUFS},
	{syscall.ENOMEM, abi.ENOMEM},
	{syscall.EAFNOSUPPORT, abi.EAFNOSUPPORT},
	{syscall.EOPNOTSUPP, abi.EOPNOTSUPP},
	{syscall.EINVAL, abi.EINVAL},
	{syscall.EBADF, abi.EBADF},
	{syscall.EINTR, abi.EINTR},
	{syscall.EFAULT, abi.EFAULT},
}

// errnoOf maps a Go syscall error to its internal MicroPython error
// counterpart (see micropython/py/mperrno.h).
func errnoOf(err error) int32 {
	if err == nil {
		return STATUS_OK
	}

	switch {
	case errors.Is(err, context.Canceled):
		return -abi.ECANCELED
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return -abi.ETIMEDOUT
	case errors.Is(err, net.ErrClosed):
		return -abi.EBADF
	case errors.Is(err, os.ErrPermission):
		return -abi.EACCES
	case errors.Is(err, os.ErrInvalid):
		return -abi.EINVAL
	}

	for _, e := range hostErrnos {
		if errors.Is(err, e.host) {
			return -e.guest
		}
	}

	// Checked before DNSError, which is itself a net.Error: a lookup that timed
	// out is more usefully a timeout than an unreachable host.
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return -abi.ETIMEDOUT
	}

	// py/mperrno.h carries no EAI_ codes, so every name that fails to resolve
	// reaches Python as the same OSError regardless of why.
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return -abi.EHOSTUNREACH
	}

	return -abi.EIO
}
