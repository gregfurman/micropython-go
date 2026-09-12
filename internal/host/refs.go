package host

import (
	"errors"
	"math"
	"runtime"
	"sync"

	"github.com/gregfurman/micropython-go/internal/value"
)

var (
	ErrStaleRef    = errors.New("micropython: reference is not valid for this interpreter")
	ErrRefOverflow = errors.New("micropython: too many reference acquisitions")
)

const (
	refIndexBits = 20
	refIndexMask = uint32(1<<refIndexBits - 1)
	refGenMask   = uint32(0xfff)
	refMaxSlots  = refIndexMask - 1 // reserve the all-ones ID for failure

	firstRefGeneration = uint32(1)
	invalidRefID       = int32(-1)
)

type refSlot struct {
	addr  uint32
	gen   uint32
	count uint32
}

// OwnedReferences counts acquisitions; the guest root list owns the actual
// Python pins. Guest-facing methods run under the instance lock. The mutex
// also protects the queue written by asynchronous Go cleanups.
type OwnedReferences struct {
	mu sync.Mutex

	release func(id uint32)

	pending []uint32

	slots  []refSlot
	byAddr map[uint32]uint32
	free   []uint32
}

func (o *OwnedReferences) Acquire(addr uint32) int32 {
	if addr == 0 {
		return invalidRefID
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if o.byAddr == nil {
		o.byAddr = make(map[uint32]uint32)
	}

	if index, ok := o.byAddr[addr]; ok {
		if o.slots[index].count == math.MaxUint32 {
			return invalidRefID
		}

		o.slots[index].count++

		return int32(o.refID(index))
	}

	index, ok := o.alloc(addr)
	if !ok {
		return invalidRefID
	}

	o.byAddr[addr] = index

	return int32(o.refID(index))
}

func (o *OwnedReferences) alloc(addr uint32) (uint32, bool) {
	if len(o.free) > 0 {
		index := o.free[len(o.free)-1]
		o.free = o.free[:len(o.free)-1]

		slot := &o.slots[index]
		slot.addr = addr
		slot.count = 1

		return index, true
	}

	if len(o.slots) == 0 {
		o.slots = append(o.slots, refSlot{
			gen: firstRefGeneration,
		})
	}

	if len(o.slots) > int(refMaxSlots) {
		return 0, false
	}

	index := uint32(len(o.slots))
	o.slots = append(o.slots, refSlot{
		addr:  addr,
		gen:   firstRefGeneration,
		count: 1,
	})

	return index, true
}

// Release gives back one acquisition, unpinning the guest object behind the
// last of them. It reports whether this was that last one.
func (o *OwnedReferences) Release(id uint32) bool {
	if !o.drop(id) {
		return false
	}

	if o.release != nil {
		o.release(id)
	}

	return true
}

// Free gives back every acquisition of one reference at once and unpins the
// object in the guest, however many handles were sharing it.
func (o *OwnedReferences) Free(id uint32) bool {
	o.mu.Lock()

	index, slot, ok := o.slotFor(id)
	if !ok {
		o.mu.Unlock()
		return false
	}

	delete(o.byAddr, slot.addr)

	slot.addr = 0
	slot.count = 0
	slot.gen = nextGeneration(slot.gen)

	o.free = append(o.free, index)
	o.mu.Unlock()

	if o.release != nil {
		o.release(id)
	}

	return true
}

// FreeHandle is Free addressed by handle rather than by bare id, refusing one
// this table did not mint.
func (o *OwnedReferences) FreeHandle(ref *value.Ref) bool {
	if ref == nil || ref.Owner() != o {
		return false
	}

	return o.Free(ref.ID())
}

// drop gives back one acquisition without calling the guest. It reports
// whether the caller must remove the pin. Guest imports use it directly;
// host releases call it through Release, outside the reference mutex.
func (o *OwnedReferences) drop(id uint32) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	index, slot, ok := o.slotFor(id)
	if !ok {
		return false
	}

	slot.count--
	if slot.count != 0 {
		return false
	}

	delete(o.byAddr, slot.addr)

	slot.addr = 0
	slot.gen = nextGeneration(slot.gen)

	o.free = append(o.free, index)

	return true
}

func nextGeneration(gen uint32) uint32 {
	gen = (gen + 1) & refGenMask
	if gen == 0 {
		return firstRefGeneration
	}

	return gen
}

func (o *OwnedReferences) refID(index uint32) uint32 {
	return index | o.slots[index].gen<<refIndexBits
}

func (o *OwnedReferences) slotFor(id uint32) (uint32, *refSlot, bool) {
	index := id & refIndexMask
	if index == 0 || int(index) >= len(o.slots) {
		return 0, nil, false
	}

	slot := &o.slots[index]
	if slot.count == 0 || id>>refIndexBits != slot.gen {
		return 0, nil, false
	}

	return index, slot, true
}

// Retain gives a Go handle its own acquisition. Decode releases the guest's
// acquisition separately, so a handle can outlive the result arena.
func (o *OwnedReferences) Retain(id uint32) (*value.Ref, error) {
	o.mu.Lock()

	_, slot, ok := o.slotFor(id)
	if !ok {
		o.mu.Unlock()
		return nil, ErrStaleRef
	}

	if slot.count == math.MaxUint32 {
		o.mu.Unlock()
		return nil, ErrRefOverflow
	}

	slot.count++
	o.mu.Unlock()

	// track cannot fail, so the acquisition just counted always reaches a
	// handle: nothing between here and the return can strand it.
	return o.track(id), nil
}

func (o *OwnedReferences) track(id uint32) *value.Ref {
	ref := value.NewRef(id, o)

	runtime.AddCleanup(ref, func(id uint32) {
		o.mu.Lock()
		o.pending = append(o.pending, id)
		o.mu.Unlock()
	}, id)

	return ref
}

func (o *OwnedReferences) Lookup(ref *value.Ref) (uint32, error) {
	if ref == nil || ref.Owner() != o {
		return 0, ErrStaleRef
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if _, _, ok := o.slotFor(ref.ID()); !ok {
		return 0, ErrStaleRef
	}

	return ref.ID(), nil
}

func (o *OwnedReferences) Drain() {
	o.mu.Lock()
	pending := o.pending
	o.pending = nil
	o.mu.Unlock()

	for _, id := range pending {
		o.Release(id)
	}
}
