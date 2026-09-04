package host

import (
	"errors"
	"runtime"

	"github.com/gregfurman/micropython-go/internal/value"
)

// ErrStaleRef reports a guest reference that this interpreter cannot resolve,
// because it belongs to another interpreter or to a pre-Restore timeline.
var ErrStaleRef = errors.New("micropython: reference is not valid for this interpreter")

// pendingRefs bounds the references waiting to be freed. A cleanup cannot
// block, so anything dropped past this stays rooted in the guest.
const pendingRefs = 1024

type refKey struct {
	id    uint32
	epoch uint64
}

type OwnedReferences struct {
	pending chan refKey
	epoch   uint64
}

func (o *OwnedReferences) Track(id uint32) *value.Ref {
	if id == 0 {
		return nil
	}

	r := value.NewRef(id, o, o.epoch)
	runtime.AddCleanup(r, o.queueRelease, refKey{id: id, epoch: o.epoch})
	return r
}

func (o *OwnedReferences) Lookup(r *value.Ref) (uint32, error) {
	if r == nil || r.Owner() != any(o) || r.Epoch() != o.epoch {
		return 0, ErrStaleRef
	}
	return r.ID(), nil
}

func (o *OwnedReferences) Inc() {
	o.epoch++
}

func (o *OwnedReferences) queueRelease(k refKey) {
	select {
	case o.pending <- k:
	default:
	}
}

func (o *OwnedReferences) Drain(free func(id uint32)) {
	for {
		select {
		case k := <-o.pending:
			if k.epoch == o.epoch {
				free(k.id)
			}
		default:
			return
		}
	}
}
