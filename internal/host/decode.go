package host

import (
	"fmt"
)

// Values coming out of the module.
//
// build/wasm_value.c walks the result and streams it through the val_*
// callbacks at the bottom of this file. A container announces its length and
// is followed by exactly that many values, twice that for a dict, so a stack
// of partially filled frames rebuilds the tree with no intermediate
// encoding.

type frameKind int

const (
	frameList frameKind = iota
	frameTuple
	frameDict
)

type frame struct {
	kind  frameKind
	want  int // number of values still expected
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
func (d *decoder) open(kind frameKind, n int32) {
	if n == 0 {
		d.push(collapse(frame{kind: kind, items: []any{}}))
		return
	}
	want := int(n)
	if kind == frameDict {
		want *= 2
	}
	d.stack = append(d.stack, frame{kind: kind, want: want, items: make([]any, 0, want)})
}

func collapse(f frame) any {
	switch f.kind {
	case frameTuple:
		return Tuple(f.items)
	case frameDict:
		return makeMap(f.items)
	default:
		return f.items
	}
}

func makeMap(kv []any) any {
	strings := true
	for i := 0; i+1 < len(kv); i += 2 {
		if _, ok := kv[i].(string); !ok {
			strings = false
			break
		}
	}

	if strings {
		m := make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}

	m := make(map[any]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[key(kv[i])] = kv[i+1]
	}
	return m
}

func key(v any) any {
	switch v.(type) {
	case []any, map[string]any, map[any]any:
		return fmt.Sprint(v)
	default:
		return v
	}
}

func (a *ABI) Xval_none()           { a.dec.push(nil) }
func (a *ABI) Xval_bool(v int32)    { a.dec.push(v != 0) }
func (a *ABI) Xval_int(v int64)     { a.dec.push(v) }
func (a *ABI) Xval_float(v float64) { a.dec.push(v) }
func (a *ABI) Xval_list(n int32)    { a.dec.open(frameList, n) }
func (a *ABI) Xval_tuple(n int32)   { a.dec.open(frameTuple, n) }
func (a *ABI) Xval_dict(n int32)    { a.dec.open(frameDict, n) }

func (a *ABI) Xval_str(ptr, length int32) {
	a.dec.push(a.str(ptr, length))
}

func (a *ABI) Xval_bytes(ptr, length int32) {
	// Copy: the module's linear memory is reused as soon as we return.
	out := make([]byte, length)
	copy(out, a.mem()[ptr:ptr+length])
	a.dec.push(out)
}

func (a *ABI) Xval_other(typePtr, typeLen, reprPtr, reprLen int32) {
	a.dec.push(Object{
		Type: a.str(typePtr, typeLen),
		Repr: a.str(reprPtr, reprLen),
	})
}
