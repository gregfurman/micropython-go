package host

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Values going into the module: a flat prefix walk, one byte of tag per value,
// decoded by build/wasm_build.c. Tags and depth must match it.

const (
	pkNone  = 0
	pkFalse = 1
	pkTrue  = 2
	pkInt   = 3
	pkFloat = 4
	pkStr   = 5
	pkBytes = 6
	pkList  = 7
	pkTuple = 8
	pkDict  = 9

	pkSet       = 10
	pkFrozenSet = 11
)

const maxEncodeDepth = 32

// encoder is the mirror of decoder. It is NOT thread safe.
type encoder struct {
	buf []byte
}

func (e *encoder) reset() { e.buf = e.buf[:0] }

func (e *encoder) tag(t byte)   { e.buf = append(e.buf, t) }
func (e *encoder) u32(v uint32) { e.buf = binary.LittleEndian.AppendUint32(e.buf, v) }
func (e *encoder) u64(v uint64) { e.buf = binary.LittleEndian.AppendUint64(e.buf, v) }

func (e *encoder) blob(s string)      { e.u32(uint32(len(s))); e.buf = append(e.buf, s...) }
func (e *encoder) blobBytes(b []byte) { e.u32(uint32(len(b))); e.buf = append(e.buf, b...) }

func (e *encoder) value(v any, depth int) error {
	if depth > maxEncodeDepth {
		return fmt.Errorf("micropython: argument nested deeper than %d levels", maxEncodeDepth)
	}

	switch value := v.(type) {
	case nil:
		e.tag(pkNone)
		return nil
	case bool:
		if value {
			e.tag(pkTrue)
		} else {
			e.tag(pkFalse)
		}
		return nil
	case int:
		e.int64(int64(value))
		return nil
	case int8:
		e.int64(int64(value))
		return nil
	case int16:
		e.int64(int64(value))
		return nil
	case int32:
		e.int64(int64(value))
		return nil
	case int64:
		e.int64(value)
		return nil
	case uint8:
		e.int64(int64(value))
		return nil
	case uint16:
		e.int64(int64(value))
		return nil
	case uint32:
		e.int64(int64(value))
		return nil
	case uint:
		return e.uint64(uint64(value))
	case uint64:
		return e.uint64(value)
	case uintptr:
		return e.uint64(uint64(value))

	case float32:
		e.float64(float64(value))
		return nil
	case float64:
		e.float64(value)
		return nil

	case json.Number:
		return e.number(value)

	case string:
		e.tag(pkStr)
		e.blob(value)
		return nil
	case []byte:
		e.tag(pkBytes)
		e.blobBytes(value)
		return nil

	case Tuple:
		e.tag(pkTuple)
		e.u32(uint32(len(value)))
		return elements(e, value, depth)
	case Set:
		// Duplicates and unhashable items are the guest's problem
		e.tag(pkSet)
		e.u32(uint32(len(value)))
		return elements(e, value, depth)
	case FrozenSet:
		e.tag(pkFrozenSet)
		e.u32(uint32(len(value)))
		return elements(e, value, depth)
	case []any:
		return encodeList(e, value, depth)

	case map[string]any:
		e.tag(pkDict)
		e.u32(uint32(len(value)))
		for k, item := range value {
			e.tag(pkStr)
			e.blob(k)
			if err := e.value(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	if handled, err := e.fastSlice(v, depth); handled {
		return err
	}

	blob, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("micropython: cannot pass %T to Python: %w", v, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(blob))
	decoder.UseNumber()

	var standard any
	if err := decoder.Decode(&standard); err != nil {
		return fmt.Errorf("micropython: cannot parse %T to Python: %w", v, err)
	}

	return e.value(standard, depth)
}

func (e *encoder) fastSlice(v any, depth int) (bool, error) {
	switch s := v.(type) {
	case []string:
		return true, encodeList(e, s, depth)
	case []int:
		return true, encodeList(e, s, depth)
	case []int64:
		return true, encodeList(e, s, depth)
	case []float64:
		return true, encodeList(e, s, depth)
	case []bool:
		return true, encodeList(e, s, depth)
	case []map[string]any:
		return true, encodeList(e, s, depth)
	case [][]byte:
		return true, encodeList(e, s, depth)
	}
	return false, nil
}

func (e *encoder) int64(v int64) {
	e.tag(pkInt)
	e.u64(uint64(v))
}

func (e *encoder) uint64(v uint64) error {
	if v > math.MaxInt64 {
		return fmt.Errorf("micropython: %d is too large to pass as an int", v)
	}
	e.int64(int64(v))
	return nil
}

func (e *encoder) float64(v float64) {
	e.tag(pkFloat)
	e.u64(math.Float64bits(v))
}

func (e *encoder) number(n json.Number) error {
	s := n.String()

	if !strings.ContainsAny(s, ".eE") {
		v, err := n.Int64()
		if err != nil {
			return fmt.Errorf("micropython: %s does not fit in a Python int: %w", s, err)
		}
		e.int64(v)
		return nil
	}

	f, err := n.Float64()
	if err != nil {
		return fmt.Errorf("micropython: %s is not a number: %w", s, err)
	}
	e.float64(f)
	return nil
}

func elements[T any, S ~[]T](e *encoder, s S, depth int) error {
	for _, item := range s {
		if err := e.value(item, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func encodeList[T any, S ~[]T](e *encoder, s S, depth int) error {
	e.tag(pkList)
	e.u32(uint32(len(s)))
	return elements(e, s, depth)
}
