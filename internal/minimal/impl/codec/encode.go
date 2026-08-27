package codec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"github.com/gregfurman/micropython-go/internal/minimal/impl/memory"
)

func (c *Codec) EncodeError(ptr int32, err error) error {
	// TODO: should this be a HostError?
	typ, msg := "RuntimeError", err.Error()

	if pe, ok := errors.AsType[*PythonError](err); ok {
		if pe.Type != "" {
			typ = pe.Type
		}
		msg = pe.Msg
	}
	if strings.ContainsRune(typ, '\x04') {
		return fmt.Errorf("exception type %q contains the field separator", typ)
	}
	return c.putBlob(ptr, KindException, []byte(typ+"\x04"+msg))
}

func (c *Codec) EncodeEmptyError(ptr int32) error {
	return c.putWords(ptr, KindException, 0, 0)
}

func (c *Codec) encodeAt(ptr int32, v any) error {
	switch x := v.(type) {
	case nil:
		return c.putWords(ptr, KindNone, 0, 0)
	case bool:
		var w uint32
		if x {
			w = 1
		}
		return c.putWords(ptr, KindBool, w, 0)
	case int:
		return c.encodeAt(ptr, int64(x))
	case int32:
		return c.putWords(ptr, KindInt, uint32(x), 0)
	case int64:
		if x < math.MinInt32 || x > math.MaxInt32 {
			return c.putBlob(ptr, KindBigint, []byte(strconv.FormatInt(x, 10)))
		}
		return c.putWords(ptr, KindInt, uint32(int32(x)), 0)
	case float64:
		bits := math.Float64bits(x)
		return c.putWords(ptr, KindFloat, uint32(bits), uint32(bits>>32))
	case string:
		return c.putBlob(ptr, KindStr, []byte(x))
	case []byte:
		return c.putBlob(ptr, KindBytes, x)
	case *big.Int:
		return c.putBlob(ptr, KindBigint, []byte(x.String()))
	case float32:
		return c.encodeAt(ptr, float64(x))
	case uint:
		return c.encodeAt(ptr, int64(x))
	case uint32:
		return c.encodeAt(ptr, int64(x))
	case uint64:
		return c.encodeAt(ptr, int64(x)) // int64 case will promote to BigInt if it overflows int32
	}

	// TODO: extract type from the function signature for this
	rv := reflect.ValueOf(v)
	switch rv.Kind() {

	case reflect.Slice, reflect.Array:
		n := rv.Len()
		if n == 0 {
			return c.putWords(ptr, KindList, 0, 0)
		}
		buf := c.mem.Alloc(int32(n))
		if buf == 0 {
			return memory.ErrGuestOOM
		}
		for k := range n {
			if err := c.encodeAt(buf+int32(k)*ValueSize, rv.Index(k).Interface()); err != nil {
				c.mem.Free(buf)
				return err
			}
		}
		return c.putWords(ptr, KindList, uint32(n), uint32(buf))

	case reflect.Map:
		n := rv.Len()
		if n == 0 {
			return c.putWords(ptr, KindDict, 0, 0)
		}
		buf := c.mem.Alloc(int32(n) * ValueSize * 2)
		if buf == 0 {
			return memory.ErrGuestOOM
		}
		k := int32(0)
		for iter := rv.MapRange(); iter.Next(); k++ {
			keyPtr := buf + (k * ValueSize * 2)
			if err := c.encodeAt(keyPtr, iter.Key().Interface()); err != nil {
				c.mem.Free(buf)
				return err
			}
			if err := c.encodeAt(keyPtr+ValueSize, iter.Value().Interface()); err != nil {
				c.mem.Free(buf)
				return err
			}
		}
		return c.putWords(ptr, KindDict, uint32(n), uint32(buf))

	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return c.putWords(ptr, KindNone, 0, 0)
		}
		return c.encodeAt(ptr, rv.Elem().Interface())
	}

	return fmt.Errorf("cannot encode %T", v)
}

func (c *Codec) putWords(ptr int32, kind Kind, w1, w2 uint32) error {
	b, err := c.mem.View(ptr, 12)
	if err != nil {
		return err
	}
	binary.LittleEndian.PutUint32(b[0:], uint32(kind))
	binary.LittleEndian.PutUint32(b[4:], w1)
	binary.LittleEndian.PutUint32(b[8:], w2)
	return nil
}

// putBlob mallocs in guest memory and copies data in. The guest frees it.
func (c *Codec) putBlob(ptr int32, kind Kind, data []byte) error {
	n := len(data)
	if n == 0 {
		return c.putWords(ptr, kind, 0, 0)
	}
	p := c.mem.Alloc(int32(n))
	if p == 0 {
		return errors.New("guest malloc failed")
	}

	buf, err := c.mem.View(p, int32(n))
	if err != nil {
		return err
	}
	copy(buf, data)
	return c.putWords(ptr, kind, uint32(n), uint32(p))
}
