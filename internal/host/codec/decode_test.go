package codec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"slices"
	"testing"

	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/value"
)

type testRefs struct {
	retained  []uint32
	released  []uint32
	onRelease func()
	retainErr error
}

func (r *testRefs) Retain(id uint32) (*value.Ref, error) {
	if r.retainErr != nil {
		return nil, r.retainErr
	}
	r.retained = append(r.retained, id)
	return value.NewRef(id, r), nil
}

func (r *testRefs) Lookup(ref *value.Ref) (uint32, error) { return ref.ID(), nil }

func (r *testRefs) Release(id uint32) bool {
	r.released = append(r.released, id)
	if r.onRelease != nil {
		r.onRelease()
	}
	return true
}

// A tuple with three opaque occurrences, including two of the same object.
func decodeFixture(t *testing.T) (*Codec, *testRefs, []byte) {
	t.Helper()
	m := memory.New(1, memory.MaxPages)
	refs := &testRefs{}
	b, err := m.View(64, 68)
	if err != nil {
		t.Fatal(err)
	}
	root := Value{Kind: KindTuple, W1: 3, W2: 84}
	root.MarshalWords(b)
	binary.LittleEndian.PutUint32(b[12:], 120)
	binary.LittleEndian.PutUint32(b[16:], 3)
	for i, id := range []uint32{7, 9, 7} {
		v := Value{Kind: KindObject, W1: id}
		v.MarshalWords(b[20+i*ValueSize:])
		binary.LittleEndian.PutUint32(b[56+i*4:], id)
	}
	return New(m, refs), refs, b
}

func TestDecodeRetainsHandlesAndReleasesGuestIDs(t *testing.T) {
	c, refs, region := decodeFixture(t)
	before := bytes.Clone(region)
	decoded, err := c.Decode(64, int32(len(region)))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(refs.retained, []uint32{7, 9, 7}) {
		t.Fatalf("retained = %v", refs.retained)
	}
	if !slices.Equal(refs.released, []uint32{7, 7, 9}) {
		t.Fatalf("released = %v, want every acquisition once", refs.released)
	}
	if !bytes.Equal(before, region) {
		t.Fatal("Decode changed guest bytes")
	}
	items := decoded.(value.TupleValue)
	if items[0].(value.Object).Ref() != 7 || items[2].(value.Object).Ref() != 7 {
		t.Fatal("decoded handles lost their IDs")
	}
}

func TestDecodeFailureReleasesAllGuestIDs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]byte, *testRefs)
	}{
		{"object kind", func(b []byte, _ *testRefs) { binary.LittleEndian.PutUint32(b[32:], uint32(KindInvalid)) }},
		{"root kind", func(b []byte, _ *testRefs) { binary.LittleEndian.PutUint32(b, uint32(KindInvalid)) }},
		{"outside region", func(b []byte, _ *testRefs) { binary.LittleEndian.PutUint32(b[8:], 256) }},
		{"unaligned block", func(b []byte, _ *testRefs) { binary.LittleEndian.PutUint32(b[8:], 85) }},
		{"huge count", func(b []byte, _ *testRefs) { binary.LittleEndian.PutUint32(b[4:], ^uint32(0)) }},
		{"cycle", func(b []byte, _ *testRefs) { binary.LittleEndian.PutUint32(b[8:], 64) }},
		{"foreign reference", func(b []byte, _ *testRefs) { binary.LittleEndian.PutUint32(b[24:], 11) }},
		{"retain failure", func(_ []byte, r *testRefs) { r.retainErr = errors.New("cannot retain") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, refs, region := decodeFixture(t)
			tc.mutate(region, refs)
			if _, err := c.Decode(64, int32(len(region))); err == nil {
				t.Fatal("invalid tree decoded successfully")
			}
			if !slices.Equal(refs.released, []uint32{7, 7, 9}) {
				t.Fatalf("released = %v, want all guest acquisitions", refs.released)
			}
			if slices.Contains(refs.retained, 11) {
				t.Fatal("retained a reference absent from the ledger")
			}
		})
	}
}

func TestReleaseRefsDoesNotReadTree(t *testing.T) {
	c, refs, region := decodeFixture(t)
	clear(region[:ValueSize])
	refs.onRelease = func() {
		c.mem.Grow(1, memory.MaxPages)
		// Releases may enter the guest: no remaining ID may alias its memory.
		b, err := c.mem.View(64, int32(len(region)))
		if err != nil {
			t.Fatal(err)
		}
		clear(b)
	}
	if err := c.ReleaseRefs(64, int32(len(region))); err != nil {
		t.Fatal(err)
	}
	if len(refs.retained) != 0 || !slices.Equal(refs.released, []uint32{7, 7, 9}) {
		t.Fatalf("retained %v, released %v", refs.retained, refs.released)
	}
}

func TestDecodeRejectsInvalidLedger(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ptr, count uint32
	}{
		{"outside region", 256, 3},
		{"inside header", 64, 3},
		{"unaligned", 121, 2},
		{"huge count", 120, ^uint32(0)},
		{"noncanonical empty", 120, 0},
		{"null pointer", 0, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, refs, b := decodeFixture(t)
			binary.LittleEndian.PutUint32(b[12:], tc.ptr)
			binary.LittleEndian.PutUint32(b[16:], tc.count)
			if _, err := c.Decode(64, int32(len(b))); err == nil {
				t.Fatal("invalid ledger accepted")
			}
			if len(refs.released) != 0 || len(refs.retained) != 0 {
				t.Fatal("invalid ledger changed reference ownership")
			}
		})
	}
}
