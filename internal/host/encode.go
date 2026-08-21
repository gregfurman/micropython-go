package host

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
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

func (e *encoder) tag(t byte)    { e.buf = append(e.buf, t) }
func (e *encoder) u32(v uint32)  { e.buf = binary.LittleEndian.AppendUint32(e.buf, v) }
func (e *encoder) u64(v uint64)  { e.buf = binary.LittleEndian.AppendUint64(e.buf, v) }
func (e *encoder) blob(s string) { e.u32(uint32(len(s))); e.buf = append(e.buf, s...) }

func (e *encoder) value(v any, depth int) error {
	if depth > maxEncodeDepth {
		return fmt.Errorf("micropython: argument nested deeper than %d levels", maxEncodeDepth)
	}

	switch value := v.(type) {
	case nil:
		e.tag(pkNone)
	case bool:
		if value {
			e.tag(pkTrue)
		} else {
			e.tag(pkFalse)
		}

	case int:
		e.int64(int64(value))
	case int8:
		e.int64(int64(value))
	case int16:
		e.int64(int64(value))
	case int32:
		e.int64(int64(value))
	case int64:
		e.int64(value)
	case uint8:
		e.int64(int64(value))
	case uint16:
		e.int64(int64(value))
	case uint32:
		e.int64(int64(value))

	case float32:
		e.float64(float64(value))
	case float64:
		e.float64(value)

	case json.Number:
		// From the fallback below, which decodes with UseNumber so that whole
		// numbers stay integers instead of becoming float64 the way plain
		// json.Unmarshal into any would make them.
		if n, err := value.Int64(); err == nil {
			e.int64(n)
			return nil
		}
		f, err := value.Float64()
		if err != nil {
			return fmt.Errorf("micropython: %s is not a number: %w", value, err)
		}
		e.float64(f)

	case string:
		e.tag(pkStr)
		e.blob(value)
	case []byte:
		e.tag(pkBytes)
		e.blob(string(value))

	case Tuple:
		e.tag(pkTuple)
		e.u32(uint32(len(value)))
		return e.each(value, depth)
	case Set:
		// Duplicates and unhashable items are the guest's problem, and it
		// gives the same answers set() would: the first collapses, the second
		// is a TypeError.
		e.tag(pkSet)
		e.u32(uint32(len(value)))
		return e.each(value, depth)
	case FrozenSet:
		e.tag(pkFrozenSet)
		e.u32(uint32(len(value)))
		return e.each(value, depth)
	case []any:
		e.tag(pkList)
		e.u32(uint32(len(value)))
		return e.each(value, depth)

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

	default:
		// Anything the cases above do not name -- structs, named types,
		// pointers, concrete containers like []int -- reaches Python through
		// JSON, which gets field names and tags right for a fraction of the
		// code a reflective walk would take.
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
		return e.value(standard, depth+1)
	}

	return nil
}

func (e *encoder) int64(v int64) {
	e.tag(pkInt)
	e.u64(uint64(v))
}

func (e *encoder) float64(v float64) {
	e.tag(pkFloat)
	e.u64(math.Float64bits(v))
}

func (e *encoder) each(items []any, depth int) error {
	for _, item := range items {
		if err := e.value(item, depth+1); err != nil {
			return err
		}
	}
	return nil
}
