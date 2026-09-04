package codec

import (
	"fmt"
	"math"

	"github.com/gregfurman/micropython-go/internal/value"
)

// Container is a guest container the host holds a handle to, read back an
// element at a time. The guest hands one over rather than copying it out,
// since a container reachable from itself has no finite copy.
type Container struct {
	Kind Kind
	Len  int32
	Ref  uint32
}

func container(v Value) (Container, bool, error) {
	switch v.Kind {
	case KindList, KindTuple, KindDict, KindSet, KindFrozenSet:
	default:
		return Container{}, false, nil
	}
	if v.W1 > math.MaxInt32 {
		return Container{}, true, fmt.Errorf("container length %d out of range", v.W1)
	}
	return Container{Kind: v.Kind, Len: int32(v.W1), Ref: v.W2}, true, nil
}

// IsMap reports whether the elements are read with MapNext rather than SeqItem.
func (c Container) IsMap() bool {
	return c.Kind == KindDict || c.Kind == KindSet || c.Kind == KindFrozenSet
}

func (c Container) Type() string {
	switch c.Kind {
	case KindTuple:
		return "tuple"
	case KindDict:
		return "dict"
	case KindSet:
		return "set"
	case KindFrozenSet:
		return "frozenset"
	default:
		return "list"
	}
}

// Repr is what Python prints for a container it is already inside of, which is
// the only shape a handed-over container is printed in.
func (c Container) Repr() string {
	switch c.Kind {
	case KindDict:
		return "{...}"
	case KindTuple:
		return "(...)"
	default:
		return "[...]"
	}
}

func (c Container) Build(items []value.Value, entries []value.Item) value.Value {
	switch c.Kind {
	case KindTuple:
		return value.NewTuple(items...)
	case KindSet:
		return value.NewSet(items...)
	case KindFrozenSet:
		return value.NewFrozenSet(items...)
	case KindDict:
		return value.NewDict(entries...)
	default:
		return value.NewList(items...)
	}
}
