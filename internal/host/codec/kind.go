package codec

import "github.com/gregfurman/micropython-go/internal/host/abi"

type Kind int32

const (
	KindInvalid Kind = abi.KindInvalid // never written; catches unwritten buffers
	KindNull    Kind = abi.KindNull    // no value / missing value
	KindNone    Kind = abi.KindNone    // Python None
	KindBool    Kind = abi.KindBool    // w1 = 0|1
	KindInt     Kind = abi.KindInt     // w1..w2 = int64
	KindBigint  Kind = abi.KindBigint  // w1 = len, w2 = ptr (decimal ASCII)
	KindFloat   Kind = abi.KindFloat   // w1..w2 = IEEE-754 float64
	KindStr     Kind = abi.KindStr     // w1 = len, w2 = ptr
	KindBytes   Kind = abi.KindBytes   // w1 = len, w2 = ptr
	KindTuple   Kind = abi.KindTuple   // w1 = len, w2 = ptr to w1 values
	KindList    Kind = abi.KindList    // as KindTuple
	KindDict    Kind = abi.KindDict    // w1 = pairs, w2 = ptr to w1*2 values (k, v, k, v)
	KindObject  Kind = abi.KindObject  // w1 = ref, w2 = object-info ptr | attributes
	KindRef     Kind = abi.KindRef     // host -> guest only: w1 = ref
	// w1 = len, w2 = ptr. Out: type \x04 str(exc) \x04 traceback. In: type \x04 message.
	KindException Kind = abi.KindException
	KindSet       Kind = abi.KindSet       // as KindTuple
	KindFrozenSet Kind = abi.KindFrozenSet // as KindTuple
)

const (
	KindObjectIterable uint32 = abi.KindObjectIterable
	KindObjectCallable uint32 = abi.KindObjectCallable
	KindObjectAttrMask        = KindObjectIterable | KindObjectCallable
)
