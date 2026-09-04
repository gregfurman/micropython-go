//go:build ignore

package abi

/*
#cgo CFLAGS: -m32
#cgo CFLAGS: -I${SRCDIR}/../../../build -I${SRCDIR}/../../../build/build-embed
#cgo CFLAGS: -I${SRCDIR}/../../../micropython -I${SRCDIR}/../../../micropython/ports/embed
#include "types.h"
*/
import "C"

const (
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
