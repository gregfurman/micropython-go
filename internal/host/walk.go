package host

import (
	"fmt"
	"slices"

	"github.com/gregfurman/micropython-go/internal/host/codec"
	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/value"
)

// maxWalkDepth bounds how far a value is copied out. Past it a container is
// handed over as a handle for Resolve to carry on from, since a Go stack
// overflow is not something a program can recover from.
const maxWalkDepth = 128

func (i *Module) consume(ptr int32) (value.Value, error) {
	return i.consumeAt(ptr, nil, 0)
}

func (i *Module) consumeAt(ptr int32, open []uint32, depth int) (value.Value, error) {
	v, box, err := i.codec.Consume(ptr)
	if err != nil || box.Kind == 0 {
		return v, err
	}
	if box.Ref == 0 {
		return nil, fmt.Errorf("%s crossed without a handle", box.Type())
	}
	return i.walk(box, open, depth)
}

// walk copies a container out. open holds the containers being walked above
// this one, so one that reaches itself is handed over instead.
func (i *Module) walk(box codec.Container, open []uint32, depth int) (value.Value, error) {
	if depth >= maxWalkDepth || slices.Contains(open, box.Ref) {
		return value.NewObject(box.Type(), box.Repr(), i.refs.Track(box.Ref), true, false), nil
	}
	defer i.mod.Xrelease_ref(int32(box.Ref))

	buf, err := i.walkBuf(depth)
	if err != nil {
		return nil, err
	}
	open = append(open, box.Ref)

	if box.IsMap() {
		return i.walkMap(box, buf, open, depth)
	}
	return i.walkSeq(box, buf, open, depth)
}

func (i *Module) walkSeq(box codec.Container, buf int32, open []uint32, depth int) (value.Value, error) {
	items := make([]value.Value, 0, box.Len)
	for k := range box.Len {
		i.mod.Xseq_item(int32(box.Ref), k, buf)
		item, err := i.consumeAt(buf, open, depth+1)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", k, err)
		}
		items = append(items, item)
	}
	return box.Build(items, nil), nil
}

// walkMap reads a dict or a set. The table is read live, so a __repr__ that
// mutates it mid-walk is seen as it stands; every read is bounds checked
// against the table as it is at that moment.
func (i *Module) walkMap(box codec.Container, buf int32, open []uint32, depth int) (value.Value, error) {
	dict := box.Kind == codec.KindDict
	items := make([]value.Value, 0, box.Len)
	entries := make([]value.Item, 0, box.Len)

	for cursor := int32(0); ; {
		slot := i.mod.Xmap_next(int32(box.Ref), cursor, buf)
		if slot == -1 {
			break
		}
		if slot < 0 {
			_, _, err := i.codec.Consume(buf)
			if err == nil {
				err = fmt.Errorf("walking from slot %d failed without an error", cursor)
			}
			return nil, err
		}
		cursor = slot + 1

		key, err := i.consumeAt(buf, open, depth+1)
		if err != nil {
			return nil, fmt.Errorf("key in slot %d: %w", slot, err)
		}
		if !dict {
			items = append(items, key)
			continue
		}

		val, err := i.consumeAt(buf+codec.ValueSize, open, depth+1)
		if err != nil {
			return nil, fmt.Errorf("value in slot %d: %w", slot, err)
		}
		entries = append(entries, value.Item{Key: key, Val: val})
	}

	return box.Build(items, entries), nil
}

// walkBuf hands out the slot one level of a walk reads into. A level still
// holds its element while the level below runs, so each needs its own.
func (i *Module) walkBuf(depth int) (int32, error) {
	for len(i.walkScratch) <= depth {
		ptr := i.mem.Alloc(2 * codec.ValueSize)
		if ptr == 0 {
			return 0, memory.ErrGuestOOM
		}
		i.walkScratch = append(i.walkScratch, ptr)
	}
	return i.walkScratch[depth], nil
}
