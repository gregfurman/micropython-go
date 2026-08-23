package host

import (
	"fmt"

	"github.com/gregfurman/micropython-wasi/internal/value"
)

// Values coming out of the module.
type frame struct {
	kind  byte
	want  int
	items []any
}

// decoder is the mirror of encoder.
type decoder struct {
	stack []frame
	root  any
	got   bool
}

func (d *decoder) reset() {
	d.stack = d.stack[:0]
	d.root = nil
	d.got = false
}

// result returns the completed value, or an error if the stream was
// incoherent.
func (d *decoder) result() (any, error) {
	if !d.got || len(d.stack) != 0 {
		return nil, fmt.Errorf("micropython: truncated value from module")
	}
	return d.root, nil
}

// push accepts one finished value, either as the root or as the next slot of
// the innermost container, closing containers that are now full.
func (d *decoder) push(v any) {
	if len(d.stack) == 0 {
		d.root = v
		d.got = true
		return
	}

	top := &d.stack[len(d.stack)-1]
	top.items = append(top.items, v)
	top.want--

	// Filling one container can fill its parent too, so recurse.
	if top.want == 0 {
		done := d.stack[len(d.stack)-1]
		d.stack = d.stack[:len(d.stack)-1]
		d.push(collapse(done))
	}
}

// open starts a container of n values, or finishes an empty one outright.
func (d *decoder) open(kind byte, n int32) {
	if n == 0 {
		d.push(collapse(frame{kind: kind, items: []any{}}))
		return
	}
	want := int(n)
	if kind == value.TagDict {
		want *= 2
	}
	d.stack = append(d.stack, frame{kind: kind, want: want, items: make([]any, 0, want)})
}

func collapse(f frame) any {
	switch f.kind {
	case value.TagTuple:
		return value.Tuple(f.items)
	case value.TagDict:
		return value.Map(f.items)
	case value.TagSet:
		return value.Set(f.items)
	case value.TagFrozenSet:
		return value.FrozenSet(f.items)
	default:
		return f.items
	}
}

func (a *ABI) Xval_none()           { a.dec.push(nil) }
func (a *ABI) Xval_bool(v int32)    { a.dec.push(v != 0) }
func (a *ABI) Xval_int(v int64)     { a.dec.push(v) }
func (a *ABI) Xval_float(v float64) { a.dec.push(v) }
func (a *ABI) Xval_list(n int32)    { a.dec.open(value.TagList, n) }
func (a *ABI) Xval_tuple(n int32)   { a.dec.open(value.TagTuple, n) }
func (a *ABI) Xval_dict(n int32)    { a.dec.open(value.TagDict, n) }

func (a *ABI) Xval_set(n, frozen int32) {
	kind := byte(value.TagSet)
	if frozen != 0 {
		kind = value.TagFrozenSet
	}
	a.dec.open(kind, n)
}

func (a *ABI) Xval_str(ptr, length int32) {
	a.dec.push(a.str(ptr, length))
}

func (a *ABI) Xval_bytes(ptr, length int32) {
	out := make([]byte, length)
	copy(out, a.mem()[ptr:ptr+length])
	a.dec.push(out)
}

func (a *ABI) Xval_exception(typePtr, typeLen, msgPtr, msgLen int32) {
	a.dec.push(value.NewException(a.str(typePtr, typeLen), a.str(msgPtr, msgLen)))
}

func (a *ABI) Xval_other(typePtr, typeLen, reprPtr, reprLen int32) {
	a.dec.push(Object{
		Type: a.str(typePtr, typeLen),
		Repr: a.str(reprPtr, reprLen),
	})
}
