package exec

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

const (
	maxUint = ^uint64(0)
	maxInt  = int64(maxUint >> 1)
	minInt  = -maxInt - 1
)

type PythonUnmarshaler interface {
	UnmarshalPython(src any) error
}

func Coerce[T any](src any) (T, error) {
	var zero T
	if src == nil {
		return zero, nil
	}

	if v, ok := src.(T); ok {
		return v, nil
	}

	if unmarshaler, ok := any(&zero).(PythonUnmarshaler); ok {
		err := unmarshaler.UnmarshalPython(src)
		return zero, err
	}

	switch target := any(&zero).(type) {
	case *int64:
		val, err := coerceInt64(src)
		if err != nil {
			return zero, err
		}
		*target = val
		return zero, nil

	case *int32:
		val, err := coerceInt64(src)
		if err != nil {
			return zero, err
		}
		if val > math.MaxInt32 || val < math.MinInt32 {
			return zero, errors.New("value is out of bounds for int32")
		}
		*target = int32(val)
		return zero, nil

	case *int:
		val, err := coerceInt64(src)
		if err != nil {
			return zero, err
		}
		if val > maxInt || val < minInt {
			return zero, errors.New("value is out of bounds for int")
		}
		*target = int(val)
		return zero, nil

	case *uint64:
		val, err := coerceUint64(src)
		if err != nil {
			return zero, err
		}
		*target = val
		return zero, nil

	case *uint32:
		val, err := coerceUint64(src)
		if err != nil {
			return zero, err
		}
		if val > math.MaxUint32 {
			return zero, errors.New("value is out of bounds for uint32")
		}
		*target = uint32(val)
		return zero, nil

	case *uint:
		val, err := coerceUint64(src)
		if err != nil {
			return zero, err
		}
		if val > maxUint { // Technically redundant for 64-bit architectures, but safe
			return zero, errors.New("value is out of bounds for uint")
		}
		*target = uint(val)
		return zero, nil

	case *float64:
		val, err := coerceFloat64(src)
		if err != nil {
			return zero, err
		}
		*target = val
		return zero, nil

	case *float32:
		val, err := coerceFloat64(src)
		if err != nil {
			return zero, err
		}
		// A float32 cast can lose precision but shouldn't panic
		*target = float32(val)
		return zero, nil

	case *bool:
		val, err := coerceBool(src)
		if err != nil {
			return zero, err
		}
		*target = val
		return zero, nil

	case *string:
		val, err := coerceString(src)
		if err != nil {
			return zero, err
		}
		*target = val
		return zero, nil

	case *[]byte:
		val, err := coerceBytes(src)
		if err != nil {
			return zero, err
		}
		*target = val
		return zero, nil
	}

	// 4. JSON bridge fallback for complex types (maps/slices into structs)
	switch src.(type) {
	case map[string]any, []any, map[any]any:
		b, err := json.Marshal(src)
		if err == nil {
			if err := json.Unmarshal(b, &zero); err == nil {
				return zero, nil
			}
		}
	}

	return zero, fmt.Errorf("type mismatch: Python returned %T, Go expected %T", src, zero)
}

// --- Exhaustive Type Coercers ---

func coerceInt64(v any) (int64, error) {
	switch t := v.(type) {
	case int:
		return int64(t), nil
	case int32:
		return int64(t), nil
	case int64:
		return t, nil
	case uint:
		return int64(t), nil
	case uint32:
		return int64(t), nil
	case uint64:
		if t > uint64(math.MaxInt64) {
			return 0, errors.New("unsigned integer too large to be cast as a signed integer")
		}
		return int64(t), nil
	case float64:
		if math.IsInf(t, 0) || math.IsNaN(t) {
			return 0, errors.New("cannot convert INF/NAN to an integer")
		}
		if t > math.MaxInt64 || t < math.MinInt64 {
			return 0, errors.New("float value is out of bounds for int64")
		}
		if t-float64(int64(t)) != 0 {
			return 0, errors.New("float value contains decimals and cannot be cast as an integer")
		}
		return int64(t), nil
	case json.Number:
		return t.Int64()
	case []byte:
		return strconv.ParseInt(string(t), 0, 64)
	case string:
		return strconv.ParseInt(t, 0, 64)
	case bool:
		if t {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("cannot coerce %T to int64", v)
}

func coerceUint64(v any) (uint64, error) {
	switch t := v.(type) {
	case uint:
		return uint64(t), nil
	case uint32:
		return uint64(t), nil
	case uint64:
		return t, nil
	case int, int32, int64:
		i := coerceNumericToInt64(t)
		if i < 0 {
			return 0, errors.New("signed integer is negative and cannot be cast as an unsigned integer")
		}
		return uint64(i), nil
	case float64:
		if t < 0 {
			return 0, errors.New("float value is negative and cannot be cast as an unsigned integer")
		}
		if math.IsInf(t, 0) || math.IsNaN(t) {
			return 0, errors.New("cannot convert INF/NAN to an unsigned integer")
		}
		if t > float64(maxUint) {
			return 0, errors.New("float value is too large to be cast as an unsigned integer")
		}
		if t-float64(uint64(t)) != 0 {
			return 0, errors.New("float value contains decimals and cannot be cast as an unsigned integer")
		}
		return uint64(t), nil
	case json.Number:
		i, err := t.Int64()
		if err != nil {
			return 0, err
		}
		if i < 0 {
			return 0, errors.New("json.Number is negative and cannot be cast as an unsigned integer")
		}
		return uint64(i), nil
	case []byte:
		return strconv.ParseUint(string(t), 0, 64)
	case string:
		return strconv.ParseUint(t, 0, 64)
	}
	return 0, fmt.Errorf("cannot coerce %T to uint64", v)
}

func coerceFloat64(v any) (float64, error) {
	switch t := v.(type) {
	case float32:
		return float64(t), nil
	case float64:
		return t, nil
	case int, int32, int64, uint, uint32, uint64:
		// Convert all integers safely. Large int64/uint64 may lose minor precision.
		return castNumericToFloat64(t), nil
	case json.Number:
		return t.Float64()
	case []byte:
		return strconv.ParseFloat(string(t), 64)
	case string:
		return strconv.ParseFloat(t, 64)
	}
	return 0, fmt.Errorf("cannot coerce %T to float64", v)
}

func coerceBool(v any) (bool, error) {
	switch t := v.(type) {
	case bool:
		return t, nil
	case int, int32, int64:
		return coerceNumericToInt64(t) != 0, nil
	case uint, uint32, uint64:
		return coerceNumericToUint64(t) != 0, nil
	case float32, float64:
		return castNumericToFloat64(t) != 0, nil
	case json.Number:
		return t.String() != "0", nil
	case []byte:
		return strconv.ParseBool(string(t))
	case string:
		return strconv.ParseBool(t)
	}
	return false, fmt.Errorf("cannot coerce %T to bool", v)
}

func coerceString(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case []byte:
		return string(t), nil
	}
	return "", fmt.Errorf("cannot coerce %T to string", v)
}

func coerceBytes(v any) ([]byte, error) {
	switch t := v.(type) {
	case string:
		return []byte(t), nil
	case []byte:
		return t, nil
	}
	return nil, fmt.Errorf("cannot coerce %T to []byte", v)
}

// --- Fast Cast Helpers ---
// These exist strictly to reduce boilerplate in the bool/float coercers.

func coerceNumericToInt64(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	}
	return 0
}

func coerceNumericToUint64(v any) uint64 {
	switch t := v.(type) {
	case uint:
		return uint64(t)
	case uint32:
		return uint64(t)
	case uint64:
		return t
	}
	return 0
}

func castNumericToFloat64(v any) float64 {
	switch t := v.(type) {
	case int:
		return float64(t)
	case int32:
		return float64(t)
	case int64:
		return float64(t)
	case uint:
		return float64(t)
	case uint32:
		return float64(t)
	case uint64:
		return float64(t)
	}
	return 0
}
