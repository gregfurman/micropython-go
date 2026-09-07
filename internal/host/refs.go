package host

import (
	"errors"
	"runtime"
	"sync"

	"github.com/gregfurman/micropython-go/internal/value"
)

var ErrStaleRef = errors.New("micropython: reference is not valid for this interpreter")

// An id is a slot index in the low bits and the slot's generation in the high
// bits, so an id that outlived its object is rejected rather than naming
// whatever took the slot next.
const (
	refIndexBits = 20
	refIndexMask = 1<<refIndexBits - 1
	refGenMask   = 0xfff
	refMaxSlots  = refIndexMask
)

type refSlot struct {
	addr  uint32
	gen   uint32
	count uint32
}

type OwnedReferences struct {
	mu      sync.Mutex
	pending []uint32

	slots  []refSlot
	byAddr map[uint32]uint32
	free   []uint32
}

func (o *OwnedReferences) id(index uint32) uint32 {
	return index | o.slots[index].gen<<refIndexBits
}

// Acquire runs re-entrantly from the guest mid-serialisation, so it must never
// reach for the instance lock: this goroutine already holds it.
func (o *OwnedReferences) Acquire(addr uint32) int32 {
	if addr == 0 {
		return -1
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.byAddr == nil {
		o.byAddr = make(map[uint32]uint32)
	}
	if index, ok := o.byAddr[addr]; ok {
		o.slots[index].count++
		return int32(o.id(index))
	}

	var index uint32
	switch {
	case len(o.free) > 0:
		index = o.free[len(o.free)-1]
		o.free = o.free[:len(o.free)-1]
		o.slots[index].addr = addr
		o.slots[index].count = 1
	default:
		if len(o.slots) == 0 {
			o.slots = append(o.slots, refSlot{gen: 1}) // slot 0 reserved
		}
		if len(o.slots) > refMaxSlots {
			return -1
		}
		index = uint32(len(o.slots))
		o.slots = append(o.slots, refSlot{addr: addr, gen: 1, count: 1})
	}

	o.byAddr[addr] = index
	return int32(o.id(index))
}

// Release reports whether that was the last acquisition, so the guest should
// drop the pin.
func (o *OwnedReferences) Release(id uint32) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	slot := o.slotFor(id)
	if slot == nil {
		return false
	}

	slot.count--
	if slot.count != 0 {
		return false
	}

	delete(o.byAddr, slot.addr)
	slot.addr = 0
	slot.gen = (slot.gen + 1) & refGenMask
	if slot.gen == 0 {
		slot.gen = 1
	}
	o.free = append(o.free, id&refIndexMask)
	return true
}

func (o *OwnedReferences) slotFor(id uint32) *refSlot {
	index := id & refIndexMask
	if index == 0 || int(index) >= len(o.slots) {
		return nil
	}
	slot := &o.slots[index]
	if slot.count == 0 || id>>refIndexBits != slot.gen {
		return nil
	}
	return slot
}

func (o *OwnedReferences) Track(id uint32) *value.Ref {
	if id == 0 {
		return nil
	}

	r := value.NewRef(id, o)

	runtime.AddCleanup(r, func(id uint32) {
		o.mu.Lock()
		o.pending = append(o.pending, id)
		o.mu.Unlock()
	}, id)

	return r
}

func (o *OwnedReferences) Lookup(r *value.Ref) (uint32, error) {
	if r == nil || r.Owner() != o {
		return 0, ErrStaleRef
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.slotFor(r.ID()) == nil {
		return 0, ErrStaleRef
	}
	return r.ID(), nil
}

func (o *OwnedReferences) Drain(unpin func(id uint32)) {
	o.mu.Lock()
	pending := o.pending
	o.pending = nil
	o.mu.Unlock()

	for _, id := range pending {
		if o.Release(id) {
			unpin(id)
		}
	}
}
