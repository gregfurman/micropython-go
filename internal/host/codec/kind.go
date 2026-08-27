package codec

import "github.com/gregfurman/micropython-go/internal/host/abi"

type Kind int32

const (
	KindInvalid   Kind = abi.KindInvalid   // never written; catches unwritten buffers
	KindNull      Kind = abi.KindNull      // no value / missing value
	KindNone      Kind = abi.KindNone      // Python None
	KindBool      Kind = abi.KindBool      // w1 = 0|1
	KindInt       Kind = abi.KindInt       // w1 = int32
	KindBigint    Kind = abi.KindBigint    // w1 = len, w2 = ptr (decimal ASCII)
	KindFloat     Kind = abi.KindFloat     // w1..w2 = IEEE-754 float64
	KindStr       Kind = abi.KindStr       // w1 = len, w2 = ptr
	KindBytes     Kind = abi.KindBytes     // w1 = len, w2 = ptr
	KindTuple     Kind = abi.KindTuple     // w1 = ref
	KindList      Kind = abi.KindList      // w1 = ref
	KindDict      Kind = abi.KindDict      // w1 = ref
	KindCallable  Kind = abi.KindCallable  // w1 = ref
	KindObject    Kind = abi.KindObject    // w1 = ref
	KindRef       Kind = abi.KindRef       // host -> guest only: w1 = ref
	KindException Kind = abi.KindException // w1 = len, w2 = ptr (type \x04 traceback)
	KindSet       Kind = abi.KindSet       // w1 = len, w2 = ptr
	KindFrozenSet Kind = abi.KindFrozenSet // w1 = len, w2 = ptr
)
