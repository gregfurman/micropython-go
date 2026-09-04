package value

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
)

// Lower converts an ordinary Go value into the closed semantic value model.
// Transport codecs consume this model; conversion itself is ABI-independent.
func Lower(v any) (Value, error) { return lower(v, 0) }

func lower(v any, depth int) (Value, error) {
	if depth > MaxDepth {
		return nil, fmt.Errorf("micropython: argument nested deeper than %d levels", MaxDepth)
	}
	if v == nil {
		return None{}, nil
	}
	if semantic, ok := v.(Value); ok {
		return semantic, nil
	}
	if object, ok := v.(Object); ok {
		return nil, fmt.Errorf("micropython: %s came from Python and cannot be passed back", object.Type())
	}

	switch x := v.(type) {
	case bool:
		return Bool(x), nil
	case int:
		return Int(x), nil
	case int8:
		return Int(x), nil
	case int16:
		return Int(x), nil
	case int32:
		return Int(x), nil
	case int64:
		return Int(x), nil
	case uint:
		return unsigned(uint64(x))
	case uint8:
		return Int(x), nil
	case uint16:
		return Int(x), nil
	case uint32:
		return Int(x), nil
	case uint64:
		return unsigned(x)
	case uintptr:
		return unsigned(uint64(x))
	case float32:
		return Float(x), nil
	case float64:
		return Float(x), nil
	case json.Number:
		return number(x)
	case string:
		return Str(x), nil
	case []byte:
		return Bytes(x), nil
	case Tuple:
		return sequence(NewTuple, []any(x), depth)
	case Set:
		return sequence(NewSet, []any(x), depth)
	case FrozenSet:
		return sequence(NewFrozenSet, []any(x), depth)
	case map[string]struct{}:
		items := make([]Value, 0, len(x))
		for key := range x {
			items = append(items, Str(key))
		}
		return NewSet(items...), nil
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return None{}, nil
		}
		return lower(rv.Elem().Interface(), depth)
	case reflect.Slice, reflect.Array:
		items := make([]Value, rv.Len())
		for i := range rv.Len() {
			item, err := lower(rv.Index(i).Interface(), depth+1)
			if err != nil {
				return nil, err
			}
			items[i] = item
		}
		return NewList(items...), nil
	case reflect.Map:
		items := make([]Item, 0, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			key, err := lower(iter.Key().Interface(), depth+1)
			if err != nil {
				return nil, err
			}
			val, err := lower(iter.Value().Interface(), depth+1)
			if err != nil {
				return nil, err
			}
			items = append(items, Item{Key: key, Val: val})
		}
		return NewDict(items...), nil
	}

	return fromJSON(v, depth)
}

func unsigned(v uint64) (Value, error) {
	if v > math.MaxInt64 {
		return nil, fmt.Errorf("micropython: %d is too large to pass as an int", v)
	}
	return Int(v), nil
}

func number(v json.Number) (Value, error) {
	if !strings.ContainsAny(v.String(), ".eE") {
		n, err := v.Int64()
		if err != nil {
			return nil, fmt.Errorf("micropython: %s does not fit in a Python int: %w", v, err)
		}
		return Int(n), nil
	}
	f, err := v.Float64()
	if err != nil {
		return nil, fmt.Errorf("micropython: %s is not a number: %w", v, err)
	}
	return Float(f), nil
}

func sequence(build func(...Value) Value, items []any, depth int) (Value, error) {
	out := make([]Value, len(items))
	for i, item := range items {
		converted, err := lower(item, depth+1)
		if err != nil {
			return nil, err
		}
		out[i] = converted
	}
	return build(out...), nil
}

func fromJSON(v any, depth int) (Value, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("micropython: cannot pass %T to Python: %w", v, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var standard any
	if err := decoder.Decode(&standard); err != nil {
		return nil, fmt.Errorf("micropython: cannot parse %T to Python: %w", v, err)
	}
	return lower(standard, depth)
}
