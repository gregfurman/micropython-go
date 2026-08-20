package exec

import (
	"fmt"

	wasi "github.com/gregfurman/micropython-wasi/internal/micropython"
)

// Object is a Python value with no Go equivalent — a function, a class, an
// arbitrary instance. Only its type and repr survive the crossing.
type Object struct {
	Type string
	Repr string
}

func (o Object) String() string { return o.Repr }

// Tuple is a Python tuple. It is distinct from []any so that the round trip
// back into Python can preserve tuple-ness, which JSON could not.
type Tuple []any

// builder reassembles the value tree that wasm_value.c streams at us.
//
// The module emits a container header carrying its length, then exactly that
// many values (twice that for a dict, alternating key and value), so a stack of
// partially-filled frames is enough to rebuild the tree without any
// intermediate encoding.
type builder struct {
	mod   *wasi.Module
	stack []frame
	root  any
	got   bool
}

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

var _ wasi.Xhost = (*builder)(nil)

func (b *builder) reset() {
	b.stack = b.stack[:0]
	b.root = nil
	b.got = false
}

// result returns the completed value, or an error if the module emitted
// something incoherent.
func (b *builder) result() (any, error) {
	if !b.got || len(b.stack) != 0 {
		return nil, fmt.Errorf("micropython: truncated value from module")
	}
	return b.root, nil
}

// push accepts one finished value, either as the root or as the next slot of
// the innermost container, closing containers that are now full.
func (b *builder) push(v any) {
	if len(b.stack) == 0 {
		b.root = v
		b.got = true
		return
	}

	top := &b.stack[len(b.stack)-1]
	top.items = append(top.items, v)
	top.want--

	// Filling one container can fill its parent too, so recurse.
	if top.want == 0 {
		done := b.stack[len(b.stack)-1]
		b.stack = b.stack[:len(b.stack)-1]
		b.push(collapse(done))
	}
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

// makeMap prefers map[string]any, which is what Go code usually wants, and
// falls back to map[any]any only when Python used non-string keys.
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

// key makes a value usable as a Go map key; slices and maps are not
// comparable, so they degrade to their formatted form.
func key(v any) any {
	switch v.(type) {
	case []any, map[string]any, map[any]any:
		return fmt.Sprint(v)
	default:
		return v
	}
}

func (b *builder) openContainer(kind frameKind, n int32) {
	if n == 0 {
		b.push(collapse(frame{kind: kind, items: []any{}}))
		return
	}
	want := int(n)
	if kind == frameDict {
		want *= 2 // keys and values arrive interleaved
	}
	b.stack = append(b.stack, frame{kind: kind, want: want, items: make([]any, 0, want)})
}

// --- the imported callbacks ------------------------------------------------

func (b *builder) Xval_none()           { b.push(nil) }
func (b *builder) Xval_bool(v int32)    { b.push(v != 0) }
func (b *builder) Xval_int(v int64)     { b.push(v) }
func (b *builder) Xval_float(v float64) { b.push(v) }
func (b *builder) Xval_list(n int32)    { b.openContainer(frameList, n) }
func (b *builder) Xval_tuple(n int32)   { b.openContainer(frameTuple, n) }
func (b *builder) Xval_dict(n int32)    { b.openContainer(frameDict, n) }

func (b *builder) Xval_str(ptr, length int32) {
	b.push(b.str(ptr, length))
}

func (b *builder) Xval_bytes(ptr, length int32) {
	// Copy: the module's linear memory is reused as soon as we return.
	out := make([]byte, length)
	copy(out, b.mem()[ptr:ptr+length])
	b.push(out)
}

func (b *builder) Xval_other(typePtr, typeLen, reprPtr, reprLen int32) {
	b.push(Object{
		Type: b.str(typePtr, typeLen),
		Repr: b.str(reprPtr, reprLen),
	})
}

// mod is filled in by Init, the same back-reference hook the other host
// interfaces use.
func (b *builder) Init(m any) { b.mod = m.(*wasi.Module) }

func (b *builder) mem() []byte { return *b.mod.Xmemory().Slice() }

func (b *builder) str(ptr, length int32) string {
	if ptr == 0 || length <= 0 {
		return ""
	}
	return string(b.mem()[ptr : ptr+length])
}
