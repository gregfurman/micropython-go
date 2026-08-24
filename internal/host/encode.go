package host

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/gregfurman/micropython-go/internal/value"
	val "github.com/gregfurman/micropython-go/internal/value"
)

// ToValue converts a Go value to the Python one it most closely resembles, otherwise
// fallback to JSON.
func ToValue(v any) (val.Value, error) { return toValue(v, 0) }

func toValue(v any, depth int) (val.Value, error) {
	if depth > val.MaxDepth {
		return nil, fmt.Errorf("micropython: argument nested deeper than %d levels", val.MaxDepth)
	}

	switch v := v.(type) {
	case nil:
		return val.None{}, nil
	case bool:
		return val.Bool(v), nil

	case int:
		return val.Int(v), nil
	case int8:
		return val.Int(v), nil
	case int16:
		return val.Int(v), nil
	case int32:
		return val.Int(v), nil
	case int64:
		return val.Int(v), nil
	case uint8:
		return val.Int(v), nil
	case uint16:
		return val.Int(v), nil
	case uint32:
		return val.Int(v), nil
	case uint:
		return toInt(uint64(v))
	case uint64:
		return toInt(v)
	case uintptr:
		return toInt(uint64(v))

	case float32:
		return val.Float(v), nil
	case float64:
		return val.Float(v), nil
	case json.Number:
		return toNumber(v)

	case string:
		return val.Str(v), nil
	case []byte:
		return val.Bytes(v), nil

	case []any:
		return seq(val.NewList, v, depth)
	case val.Tuple:
		return seq(val.NewTuple, v, depth)
	case val.Set:
		return seq(val.NewSet, v, depth)
	case val.FrozenSet:
		return seq(val.NewFrozenSet, v, depth)

	// Maps
	case map[string]struct{}:
		// Special case: treating map[string]struct{} as a Set
		items := make([]val.Value, 0, len(v))
		for k := range v {
			items = append(items, val.Str(k))
		}
		return val.NewSet(items...), nil
	case map[any]any:
		return mapToDict(v, depth)
	case map[string]any:
		return mapToDict(v, depth)
	case map[string]string: // Added this as a bonus since it's very common
		return mapToDict(v, depth)

	case val.Value:
		return v, nil

	case value.Object:
		return nil, fmt.Errorf("micropython: %s came from Python and cannot be passed back", v.Type)
	case []string:
		return sliceToList(v, depth)
	case []int:
		return sliceToList(v, depth)
	case []int64:
		return sliceToList(v, depth)
	case []float64:
		return sliceToList(v, depth)
	case []bool:
		return sliceToList(v, depth)
	case []map[string]any:
		return sliceToList(v, depth)
	case [][]byte:
		return sliceToList(v, depth)
	}

	return viaJSON(v, depth)
}

func mapToDict[K comparable, V any](m map[K]V, depth int) (val.Value, error) {
	items := make([]val.Item, 0, len(m))
	for k, v := range m {
		keyVal, err := toValue(k, depth+1)
		if err != nil {
			return nil, err
		}
		valVal, err := toValue(v, depth+1)
		if err != nil {
			return nil, err
		}
		items = append(items, val.Item{Key: keyVal, Val: valVal})
	}
	return val.NewDict(items...), nil
}

func sliceToList[T any, S ~[]T](s S, depth int) (val.Value, error) {
	out := make([]val.Value, len(s))
	for i, item := range s {
		v, err := toValue(item, depth+1)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return val.NewList(out...), nil
}

func toInt(v uint64) (val.Value, error) {
	if v > math.MaxInt64 {
		return nil, fmt.Errorf("micropython: %d is too large to pass as an int", v)
	}
	return val.Int(v), nil
}

func toNumber(n json.Number) (val.Value, error) {
	s := n.String()

	if !strings.ContainsAny(s, ".eE") {
		v, err := n.Int64()
		if err != nil {
			return nil, fmt.Errorf("micropython: %s does not fit in a Python int: %w", s, err)
		}
		return val.Int(v), nil
	}

	f, err := n.Float64()
	if err != nil {
		return nil, fmt.Errorf("micropython: %s is not a number: %w", s, err)
	}
	return val.Float(f), nil
}

// seq is used for untyped slices/arrays like []any, Tuple, Set, etc.
func seq(build func(...val.Value) val.Value, items []any, depth int) (val.Value, error) {
	out := make([]val.Value, len(items))
	for i, item := range items {
		v, err := toValue(item, depth+1)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return build(out...), nil
}

// viaJSON is the open end: anything else is whatever JSON makes of it.
func viaJSON(v any, depth int) (val.Value, error) {
	blob, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("micropython: cannot pass %T to Python: %w", v, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(blob))
	decoder.UseNumber()

	var standard any
	if err := decoder.Decode(&standard); err != nil {
		return nil, fmt.Errorf("micropython: cannot parse %T to Python: %w", v, err)
	}
	return toValue(standard, depth)
}

// writer collects lowered bytes. It is the implementation of val.Writer, and
// the only one: internal/value describes the format, this holds the buffer.
type writer struct {
	buf   []byte
	depth int
	err   error
}

// Bytes returns what was written, or the first thing that went wrong.
func (w *writer) Bytes() ([]byte, error) { return w.buf, w.err }

func (w *writer) Reset() { w.buf, w.depth, w.err = w.buf[:0], 0, nil }

func (w *writer) Tag(t byte)         { w.buf = append(w.buf, t) }
func (w *writer) U32(v uint32)       { w.buf = binary.LittleEndian.AppendUint32(w.buf, v) }
func (w *writer) U64(v uint64)       { w.buf = binary.LittleEndian.AppendUint64(w.buf, v) }
func (w *writer) Raw(b []byte)       { w.buf = append(w.buf, b...) }
func (w *writer) RawString(s string) { w.buf = append(w.buf, s...) }

func (w *writer) Enter() bool {
	if w.depth >= val.MaxDepth {
		w.Fail(fmt.Errorf("micropython: value nested deeper than %d levels", val.MaxDepth))
		return false
	}
	w.depth++
	return true
}

func (w *writer) Leave() { w.depth-- }

func (w *writer) Fail(err error) {
	if w.err == nil {
		w.err = err
	}
}

// lower is the whole job in one call: a writer, one value, the bytes.
func lower(v val.Value) ([]byte, error) {
	var w writer
	val.Lower(v, &w)
	return w.Bytes()
}
