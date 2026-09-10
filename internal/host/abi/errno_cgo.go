//go:build ignore

package abi

/*
#cgo CFLAGS: -m32
#cgo CFLAGS: -I${SRCDIR}/../../../build
#cgo CFLAGS: -I${SRCDIR}/../../../micropython
#cgo CFLAGS: -I${SRCDIR}/../../../micropython/ports/embed
#cgo CFLAGS: -I${SRCDIR}/../../../micropython/ports/embed/port
#include "py/mpconfig.h"
#undef MICROPY_PY_ERRNO
#define MICROPY_PY_ERRNO (0)
#include "py/mperrno.h"

// mperrno.h names nothing between ERANGE (34) and EOPNOTSUPP (95), yet the
// guest does raise these three: extmod/vfs_lfsx.c hands littlefs failures to
// mp_raise_OSError(-ret), and littlefs numbers its codes to match Linux. They
// cannot be read from the host's errno.h, where darwin numbers them 63, 66 and
// 62. Python sees them as a bare OSError number, as it does from any littlefs
// filesystem, so reporting one matches the guest's own filesystems rather than
// inventing a value.
#define MP_ENAMETOOLONG (36)
#define MP_ENOTEMPTY (39)
#define MP_ELOOP (40)
*/
import "C"

const (
	EPERM        = C.MP_EPERM
	ENOENT       = C.MP_ENOENT
	EINTR        = C.MP_EINTR
	EIO          = C.MP_EIO
	EBADF        = C.MP_EBADF
	EAGAIN       = C.MP_EAGAIN
	ENOMEM       = C.MP_ENOMEM
	EACCES       = C.MP_EACCES
	EFAULT       = C.MP_EFAULT
	EBUSY        = C.MP_EBUSY
	EEXIST       = C.MP_EEXIST
	EXDEV        = C.MP_EXDEV
	ENOTDIR      = C.MP_ENOTDIR
	EISDIR       = C.MP_EISDIR
	EINVAL       = C.MP_EINVAL
	ENFILE       = C.MP_ENFILE
	EMFILE       = C.MP_EMFILE
	EFBIG        = C.MP_EFBIG
	ENOSPC       = C.MP_ENOSPC
	ESPIPE       = C.MP_ESPIPE
	EROFS        = C.MP_EROFS
	ENAMETOOLONG = C.MP_ENAMETOOLONG
	ENOTEMPTY    = C.MP_ENOTEMPTY
	ELOOP        = C.MP_ELOOP
	EPIPE        = C.MP_EPIPE
	EOPNOTSUPP   = C.MP_EOPNOTSUPP
	EAFNOSUPPORT = C.MP_EAFNOSUPPORT
	EADDRINUSE   = C.MP_EADDRINUSE
	ECONNABORTED = C.MP_ECONNABORTED
	ECONNRESET   = C.MP_ECONNRESET
	ENOBUFS      = C.MP_ENOBUFS
	EISCONN      = C.MP_EISCONN
	ENOTCONN     = C.MP_ENOTCONN
	ETIMEDOUT    = C.MP_ETIMEDOUT
	ECONNREFUSED = C.MP_ECONNREFUSED
	EHOSTUNREACH = C.MP_EHOSTUNREACH // NOTE: there is no ENETUNREACH: fold it into EHOSTUNREACH.
	EALREADY     = C.MP_EALREADY
	EINPROGRESS  = C.MP_EINPROGRESS
	ECANCELED    = C.MP_ECANCELED
)
