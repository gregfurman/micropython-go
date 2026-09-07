// Package codec translates between the host's value model and the records
// MicroPython reads and writes.
//
// The codec understands the ABI and nothing else. It owns no memory, reserves
// no regions, and keeps no per-operation state: every call that needs space is
// handed an arena by its caller, and every call that needs a reference goes
// through the [Refs] the codec was built with. That is the whole boundary.
//
// # The round trip
//
// Each direction has a counterpart in the C bridge, which is where to look when
// changing either half:
//
//	host                                     guest
//	----                                     -----
//	any
//	 | value.Lower                normalise to the closed value model
//	 v
//	value.Value
//	 | Codec.EncodeInto           write a record tree into an arena
//	 v
//	mp_value_t tree  ------------------->  obj_from_value    build/decode.c
//	                                            |
//	                                       Python object
//	                                            |
//	                                       value_from_obj    build/encode.c
//	                                            v
//	value.Value  <-----------------------  mp_value_t tree + ledger
//	 ^
//	 | Codec.Decode               copy the tree, retain its handles
//
// Records are laid out by build/abi.h, and both sides bump-allocate out of an
// arena: [memory.Arena] here, arena_alloc in build/arena.c there.
//
// # What a value looks like
//
// Every value is a 12-byte record. kind decides what the two payload words
// mean; nothing else about the layout varies.
//
//	+--------+--------+--------+
//	|  kind  |   w1   |   w2   |
//	+--------+--------+--------+
//
// Scalars fit entirely in w1 and w2. Everything larger puts a length in w1 and
// a pointer in w2, aimed at bytes in the same arena: a string payload, a run of
// records for a list, alternating key and value records for a dict. An object
// with no wire form crosses as a handle instead, naming a reference in w1 and
// its Python class in w2.
//
// # What a result looks like
//
// A guest result is one contiguous region. Nothing in it points outside itself,
// so the host reads a whole value tree without following anything back into the
// guest heap.
//
//	out_ptr -> +---------------------------+
//	           | root record               |  the value the call produced
//	           | refs, num_refs            |  ------+  the ledger, below
//	           +---------------------------+        |
//	           | payloads and nested       |        |
//	           | records, bump allocated   |        |
//	           | during the walk           |        |
//	           +---------------------------+ <------+
//	           | ref id, ref id, ...       |  written last, in one step
//	           +---------------------------+
//
// The header is mp_transfer_t. The ledger is every reference the guest acquired
// while writing this result, in one flat array, written by pending_refs_commit
// in build/encode.c after the walk has finished.
//
// Host to guest arguments have no header. They are a bare run of records with a
// count passed alongside, and acquire nothing.
//
// # Who owns what
//
// A handle crossing the boundary arrives with one acquisition, taken by the
// guest and recorded in the ledger. [Codec.Decode] hands that one back and takes
// a fresh one for each handle it decodes:
//
//	Decode
//	  |
//	  +-- read the ledger, copying the ids out of guest memory
//	  |
//	  +-- defer: release every id in the ledger        (whatever happens below)
//	  |
//	  +-- walk the tree
//	        a handle -> Refs.Retain, which counts an acquisition of its own
//	                    and returns the *value.Ref that will release it
//
// So a handle the walk reached ends up owned by its Go handle, and one the walk
// never reached, because decoding failed or the record was corrupt, ends up
// owned by nobody and is released on the way out. The two traversals are
// independent by construction:
//
//	decoding reads the tree, cleanup reads only the ledger,
//	and neither changes the other's ownership state.
//
// [Codec.ReleaseRefs] is the cleanup half on its own, for a result that will
// never be decoded at all, such as the arguments to a callback the host has
// already rejected.
//
// # Using it
//
// [Codec.Decode] and [Codec.EncodeInto] are the two calls that matter.
// Everything else is a variation on them. A host operation reserves a region
// from its arena, lets the guest write into it, and decodes what came back:
//
//	defer arena.Mark()()
//
//	out, err := arena.New(capacity)
//	// ...
//	used := mod.Xcall(name, nameLen, args, nargs, out, capacity)
//	v, err := codec.Decode(out, used)
//
// Decoding copies. Strings, bytes and container contents all land in Go
// storage, so nothing the decoder returns aliases the region and the arena can
// be reset the moment [Codec.Decode] returns.
//
// Encoding goes the other way, into an arena the caller still owns. The
// destination must already be reserved from that arena so that nested payloads
// land after it rather than on top of it:
//
//	ptr, err := arena.New(ValueSize)
//	err = codec.EncodeInto(arena, ptr, v)
//
// [Codec.EncodeErrorInto] writes an error as the exception the guest should
// raise, and [Codec.EncodeEmptyErrorInto] is the fallback for when even the
// error text will not fit.
package codec
