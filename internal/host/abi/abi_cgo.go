//go:build ignore

package abi

/*
// abi.h is the wire format alone: no MicroPython headers, so this needs
// nothing on the include path but build/ itself.
#cgo CFLAGS: -m32
#cgo CFLAGS: -I${SRCDIR}/../../../build
#include "abi.h"
*/
import "C"

const (
	ValueSize    = C.sizeof_mp_value_t
	TransferSize = C.sizeof_mp_transfer_t

	KindInvalid   = C.KIND_INVALID
	KindNull      = C.KIND_NULL
	KindNone      = C.KIND_NONE
	KindBool      = C.KIND_BOOL
	KindInt       = C.KIND_INT
	KindBigint    = C.KIND_BIGINT
	KindFloat     = C.KIND_FLOAT
	KindStr       = C.KIND_STR
	KindBytes     = C.KIND_BYTES
	KindTuple     = C.KIND_TUPLE
	KindList      = C.KIND_LIST
	KindDict      = C.KIND_DICT
	KindObject    = C.KIND_OBJECT
	KindRef       = C.KIND_REF
	KindException = C.KIND_EXCEPTION
	KindSet       = C.KIND_SET
	KindFrozenSet = C.KIND_FROZENSET

	KindObjectIterable = C.KIND_OBJECT_ATTR_ITERABLE
	KindObjectCallable = C.KIND_OBJECT_ATTR_CALLABLE
)
