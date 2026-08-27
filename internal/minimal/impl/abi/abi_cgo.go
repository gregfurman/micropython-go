//go:build ignore

package abi

/*
#cgo CFLAGS: -m32
#cgo CFLAGS: -I${SRCDIR}/../../../../build/minimal -I${SRCDIR}/../../../../build/minimal/micropython_embed
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
	KindCallable  = C.KIND_CALLABLE
	KindObject    = C.KIND_OBJECT
	KindRef       = C.KIND_REF
	KindException = C.KIND_EXCEPTION
)
