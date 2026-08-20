package exec

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
)

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
)

const maxPackDepth = 32

type packer struct {
	buf []byte
}

func (p *packer) reset() { p.buf = p.buf[:0] }

func (p *packer) tag(t byte)    { p.buf = append(p.buf, t) }
func (p *packer) u32(v uint32)  { p.buf = binary.LittleEndian.AppendUint32(p.buf, v) }
func (p *packer) u64(v uint64)  { p.buf = binary.LittleEndian.AppendUint64(p.buf, v) }
func (p *packer) blob(s string) { p.u32(uint32(len(s))); p.buf = append(p.buf, s...) }

func (p *packer) value(v any, depth int) error {
	if depth > maxPackDepth {
		return fmt.Errorf("micropython: argument nested deeper than %d levels", maxPackDepth)
	}

	switch value := v.(type) {
	case nil:
		p.tag(pkNone)
	case bool:
		if value {
			p.tag(pkTrue)
		} else {
			p.tag(pkFalse)
		}

	case int:
		p.int64(int64(value))
	case int8:
		p.int64(int64(value))
	case int16:
		p.int64(int64(value))
	case int32:
		p.int64(int64(value))
	case int64:
		p.int64(value)
	case uint8:
		p.int64(int64(value))
	case uint16:
		p.int64(int64(value))
	case uint32:
		p.int64(int64(value))

	case float32:
		p.float64(float64(value))
	case float64:
		p.float64(value)

	case string:
		p.tag(pkStr)
		p.blob(value)
	case []byte:
		p.tag(pkBytes)
		p.blob(string(value))

	case Tuple:
		p.tag(pkTuple)
		p.u32(uint32(len(value)))
		return p.each(value, depth)
	case []any:
		p.tag(pkList)
		p.u32(uint32(len(value)))
		return p.each(value, depth)

	case map[string]any:
		p.tag(pkDict)
		p.u32(uint32(len(value)))
		for k, item := range value {
			p.tag(pkStr)
			p.blob(k)
			if err := p.value(item, depth+1); err != nil {
				return err
			}
		}

	default:
		// The JSON Bridge for all custom named types, pointers, arrays, structs, etc.
		blob, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("micropython: cannot pass %T to Python: %w", v, err)
		}

		var standard any
		if err := json.Unmarshal(blob, &standard); err != nil {
			return fmt.Errorf("micropython: cannot parse %T to Python: %w", v, err)
		}

		// standard is now guaranteed to be a JSON primitive, []any, or map[string]any.
		// Feed it right back into the packer.
		return p.value(standard, depth+1)
	}

	return nil
}

func (p *packer) int64(v int64) {
	p.tag(pkInt)
	p.u64(uint64(v))
}

func (p *packer) float64(v float64) {
	p.tag(pkFloat)
	p.u64(math.Float64bits(v))
}

func (p *packer) each(items []any, depth int) error {
	for _, item := range items {
		if err := p.value(item, depth+1); err != nil {
			return err
		}
	}
	return nil
}
