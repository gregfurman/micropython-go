# Architecture

`micropython-go` runs MicroPython, compiled to WebAssembly and translated to Go,
inside a Go process. This describes how a value gets from one side to the other.

`DESIGN.md` is the proposal this was built from. Where the two disagree, this one
is what the code does.

## The one rule

> A value is a fixed 12-byte record. Anything variable-sized lives in an arena
> the caller owns.

Both directions work the same way, and one party owns the memory for the whole
exchange. Nothing is freed a piece at a time.

## The value record

Twelve bytes, four-byte aligned, `_Static_assert`ed on the C side and
`ValueSize` on the Go side.

```c
typedef struct {
    uint32_t kind;
    uint32_t w1;
    uint32_t w2;
} mp_value_t;
```

What `w1` and `w2` mean depends on `kind`:

| kind | w1 | w2 |
|---|---|---|
| `NONE`, `NULL` | 0 | 0 |
| `BOOL` | 0 or 1 | 0 |
| `INT` | low 32 bits of an int64 | high 32 bits |
| `FLOAT` | low 32 bits of a float64 | high 32 bits |
| `STR`, `BYTES`, `BIGINT` | byte length | pointer to the bytes |
| `EXCEPTION` | byte length | pointer to `\x04`-separated fields, see below |
| `LIST`, `TUPLE`, `SET`, `FROZENSET` | element count | pointer to that many records |
| `DICT` | pair count | pointer to twice that many, alternating key and value |
| `OBJECT` | reference id | object-info pointer, low bits carrying attributes |

Containers are copied, not handed over. A dict needs no second structure: its
entries are just `k, v, k, v` in one run.

A zero-length payload is `w1 = 0, w2 = 0`.

Exception text is the one payload whose shape depends on direction. Out of the
guest it is `type \x04 str(exc) \x04 traceback`, since only the guest can print a
traceback. Into the guest it is `type \x04 message`, and an empty type means the
guest picks its own default, `HostError`, so a failed callback stays
distinguishable from an interpreter error.

## Arenas

An arena is a bump allocator over one span of guest linear memory
(`internal/host/memory/arena.go`). There are two kinds, and the difference is
who owns the span.

**Owning** (`mem.NewArena`). The host took the span from the guest heap and
keeps it for the life of the interpreter. A request too big for the span spills
to a separate heap allocation, tracked and freed on reset. Each interpreter has
one, 64KB.

**Viewing** (`mem.ArenaAt`). Somebody else picked the address and reclaims it
when the call returns. It cannot spill: a heap pointer would outlive the span
that was lent, and nobody on the other side would free it, so it reports
`ErrArenaFull`. This mirrors `output_arena_init` in C.

The idiom is one line per operation:

```go
defer arena.Mark()()
```

`Mark` records the bump position and returns a closure that rewinds to it,
freeing anything that spilled in between. It runs whether the operation returned
or raised, so there is no separate rollback path.

## The four paths

### 1. Guest to host: a call result

Go reserves a region and passes it in. C starts it with a 16-byte
`mp_transfer_t`: the 12-byte root value followed by a four-byte reference-list
head. It serializes all payloads, including object metadata, into the rest.

```
Module.Eval
    │  arena.Mark()  ... rewinds everything below
    │  arena.String(code)          } both from the
    │  arena.New(16KB) -> outPtr   } one 64KB span
    ▼
  Xeval(codePtr, len, outPtr, 16KB)
    │
    ▼  C: mp_arena_t over [outPtr, +16KB), offset starts at 16
  value_from_obj()  recursive, one pass
    │
    ▼  returns bytes used, or a negative status
  Codec.Consume(outPtr)
    │
    ▼  recursive read, copies payloads and claims object references
  release_transfer(outPtr)  releases unclaimed references, including on error
    │
    ▼
  value.Value            the arena can be reset the moment this returns
```

### 2. Host to guest: arguments

The same span, the same `Mark`. Go writes the whole tree, C reads it in place.

```
Module.Call
    │  arena.Mark()
    │  arena.String(name)
    │  arena.New(n * 12) -> argsPtr
    │  Codec.EncodeInto(arena, argsPtr + i*12, arg)   for each arg
    ▼
  Xcall(namePtr, ..., argsPtr, n, outPtr, 16KB)
    │
    ▼  C: obj_from_value() reads the tree, allocates MicroPython objects
  mp_call_function_n_kw
```

The guest never takes ownership of the arguments. The `Mark` reset releases
them, along with the result region, on the way out.

### 3. Host callbacks: the other way round

A Python function calling into Go reverses who owns the memory. C puts two 16KB
buffers on its own stack, one for arguments and one for the return value, and
lends the second to Go.

```
  Python calls a host function
    │
    ▼  C: value_from_obj() into arg_storage (C stack)
  host_trampoline(funcID, argsPtr, n, outPtr, outCapacity)
    │
    ▼  Go: Codec.Consume() per argument, then the host func runs
  mem.ArenaAt(outPtr, outCapacity)   viewing arena, cannot spill
    │
    ▼  Codec.EncodeInto(arena, root, result)
  C: value_release_refs() releases unclaimed argument references
    │
    ▼
  C: obj_from_value(), then raise if the root is an EXCEPTION
```

An error from the host, or a panic, is written into the same region as a
`KIND_EXCEPTION`. If the message will not fit, a fresh view of the region rewinds
the bump pointer and a bare exception is written instead, which always fits.

C also releases the argument transfer's references if serialization raises
before Go is called. Callback return values and host arguments carry existing
handles back to Python; they do not acquire new guest references.

### 4. References: things that cannot be copied

Copying is for builtin data. Anything whose identity or behaviour matters keeps
a reference instead: functions, bound methods, generators, iterators, class
instances.

```
MicroPython object ──> ref_add() ──> KIND_OBJECT ──> value.Ref (Go)
```

#### The reference table

Split across the boundary, because only one part of it has to be in the guest.

`host_refs` is a list registered with `MP_REGISTER_ROOT_POINTER`, so the guest
collector traces it. It is the pin, and it must live here: a table in Go memory
is invisible to that collector, so an object it named would be swept and the
address would dangle. Everything else about a reference is bookkeeping that
holds no object pointer, so it lives in `OwnedReferences` on the host, where it
costs no guest heap and can be unit tested.

```
        GUEST                              │          HOST (Go)
                                           │
  host_refs (GC root, a list)              │   OwnedReferences
  ┌─────┬───────────────┐                  │   slots []refSlot
  │  0  │ NULL          │ reserved         │   ┌─────┬──────────────────────┐
  ├─────┼───────────────┤                  │   │  0  │ reserved             │
  │  1  │ ●─────────────┼────┐             │   ├─────┼──────────────────────┤
  ├─────┼───────────────┤    │             │   │  1  │ addr 0x2f10  gen 1   │
  │  2  │ NULL          │    │             │   │     │ count 2              │
  └─────┴───────────────┘    │             │   ├─────┼──────────────────────┤
         this is the pin ────┤             │   │  2  │ gen 3  count 0       │
         and nothing else    │             │   └─────┴──────────────────────┘
                             │             │
                             │             │   byAddr  0x2f10 -> 1
                             │             │   free    [2]
                             ▼             │
   Python heap: <function g> at 0x2f10     │
```

The guest asks for a slot through two imports, `go_ref_add(addr)` and
`go_ref_free(id)`, the same kind of bare import as `host_poll`, which the VM
hook already calls every 256 instructions. `go_ref_add` returns the id and the
guest pins the object only when the slot was empty; `go_ref_free` reports
whether that was the last acquisition, in which case the guest drops the pin.
The host's own queued releases skip the counting round trip and call the
`release_ref` export directly, since the count was settled in Go.

Deduplication keys on the guest address, which is sound because MicroPython's
collector marks and sweeps without moving objects. One object therefore occupies
one slot however many times it crosses, which is why calling the same handle in
a loop costs nothing. An address can never be stale in `byAddr`: the entry is
removed in the same operation that lifts the pin, with no guest code in between.

The id Go carries packs the slot and the generation it had when handed out:

```
   id 1048577 = 0x100001

      31        20 19                     0
     ┌────────────┬────────────────────────┐
     │ generation │       slot index       │
     │     1      │            1           │
     └────────────┴────────────────────────┘
        12 bits             20 bits
```

`count` is the number of acquisitions not yet released: transfers, Go handles,
and cleanup callbacks whose releases have not yet been applied. The pin lifts
when it reaches zero. Being an ordinary `uint32` in Go rather than a guest small
integer, it has no interesting ceiling and no permanently pinned state.

#### Transfer ownership

Every acquisition has one owner:

1. `ref_add` increments the count. The serializer immediately records the
   acquisition in the transfer's linked list.
2. The decoder creates a `*Ref` and clears the corresponding list entry's
   reference ID. That individual acquisition now belongs to the Go handle.
3. Finishing or abandoning the transfer releases every unclaimed entry.

The list nodes are also the object metadata, allocated in the same arena:

```c
typedef struct {
    uint32_t len;   // length of type + separator + repr
    uint32_t ref;   // acquisition owned by this transfer; 0 once claimed
    uint32_t next;  // next metadata node, or 0
    char blob[];
} mp_object_info_t;
```

For results, the list head lives in `mp_transfer_t`, so Go can finish the
transfer after the guest call returns. `consumeArena` defers `release_transfer`
even if decoding fails. Iterator results use the same cleanup with their
separate status protocol. Callback arguments keep their list head on the C
stack, and C finishes that transfer when the callback returns or serialization
raises.

If serialization raises, C releases the list before resetting the arena for
the exception. If decoding fails partway through a result, unvisited objects
are released by the transfer; already claimed handles are reclaimed through
their Go cleanups. No metadata payload needs an individual `malloc` or `free`.

#### The round trip

```
      GUEST                             │            HOST (Go)
                                        │
   eval("g")                            │
     │                                  │
     ▼                                  │
   value_from_obj(<function g>)         │
     │   a function cannot be copied    │
     ▼                                  │
   ref_add(obj)                         │
     │   go_ref_add(0x2f10) ────────────┼──▶ slot 1, gen 1, count 1
     │   host_refs[1] = <function g>    │  <- pinned here, and only here
     ▼                                  │
   KIND_OBJECT w1 = 0x100001 ───────────┼──▶ Codec.decode
                                        │      │
                                        │      ▼
                                        │    refs.Track(0x100001)
                                        │      │  runtime.AddCleanup
                                        │      │  clear transfer entry's ref
                                        │      ▼
                                        │    value.Object{ *Ref }
                                        │      ⋮
                                        │      ⋮   caller drops the last copy
                                        │      ▼
                                        │    cleanup fires, queues the id
                                        │      │
                                        │      ▼   start of the next operation
                                        │    Begin() drains pending releases
   release_ref(0x100001) ◀──────────────┼──────┘
     │                                  │
     ▼                                  │
   host_refs[1] = NULL                  │  <- pin lifted
     │   the count reached zero in Go,  │
     │   which bumped the slot to gen 2 │  <- id 0x100001 now rejected
     │   and returned it to the free    │
     │   list before calling in here    │
     │                                  │
     ▼                                  │
   <function g> is ordinary garbage     │
```

Everything the guest does here is bookkeeping about its own objects. Nothing in
this table names a Go value, which is why there is no mirror of it on the host
to Python side: values going that way are copied and the originals are free
immediately.

#### When a handle is finished with

A handle is finished with once no reachable Go value names it. That is a
property of the whole Go heap, so only the collector can report it: `Track` sees
a handle being born, and nothing in Go announces one dying. Hence the cleanup.

The cleanup runs asynchronously and only appends the ID to its owner's queue.
`Module.Begin` calls `ReleasePendingRefs` under the instance lock before the
next operation uses any handles. Snapshot creation also drains the queue.
Neither method runs a collector. Queuing during a guest call is safe because
the queue is not drained until that call has finished.

A mutex protects the pending slice. `Drain` detaches the whole slice under the
lock, then releases references outside it. `Track`, `Lookup`, and `Drain` are
serialized by the instance lock; cleanup goroutines only access the queue.

The distinction that matters, and the one easy to get wrong: ids are queued when
a handle is **collected**, never when one is **minted**. Queueing in `Track`
turns the queue from "handles Go has finished with" into "handles Go has ever
been given", and draining that releases references the caller is still holding.
The guest then fails the next call through them with `ValueError: stale ref`.

Each `*Ref` owns one acquisition. Copies of an `Object` share that pointer;
serializing the same Python object again creates another acquisition. While a
handle is reachable, its acquisition keeps the guest slot pinned.

#### Restore and stale handles

A handle contains an ID and an owner pointer. `Restore` loads guest memory,
clears the restored reference table, and creates a fresh `OwnedReferences` for
the module and codec. `Lookup` checks owner identity, rejecting both handles
from other interpreters and handles from before the restore.

Old cleanups still append to the old owner's queue. The restored module never
drains that queue, so an old release cannot affect a reused ID in the new
timeline. The old owner contains no reference to the module or guest memory.
No handle, owner, or queued release needs an epoch field.

Guest ids also carry a generation, bumped when a slot is freed. Nothing in
normal operation can present a released id, so this is a backstop rather than a
live defence: it rejects a stale generation rather than calling whatever
object took the slot next. Generations wrap after 4,095 uses, so they are a
bounded check; owner identity provides timeline isolation.

Two containers also cross as references rather than copies:

- one reachable from itself, which has no finite copy
- one deeper than `MP_VALUE_MAX_DEPTH`

Both are detected in C while serializing, using a list of the containers
currently being written that threads through the C stack. Their description is
written directly (`[...]`, `{...}`, `(...)`) rather than printed, because
printing a container recurses over exactly what was declined. `Resolve` reads
one back a level at a time.

So the two lifetimes are:

```
copied data      -> arena lifetime, ends at Mark reset
opaque identity  -> reference lifetime, ends when Go drops the handle
```

## Conversion

`value.Lower` is the only place an arbitrary Go value becomes a Python one. It
owns the numeric conversions, the reflect fallback for slices and maps, the JSON
fallback for everything else, and a nesting limit of `value.MaxDepth` (32).

The codec starts from the closed model `Lower` produces, so the ABI layer is a
switch over about fourteen concrete types with no reflection in it.

```
any ──> value.Lower ──> value.Value ──> Codec.EncodeInto ──> bytes in the arena
```

Decoding goes the other way and returns Go-owned values only: strings become Go
strings, byte slices are copied, children become Go collections. Nothing it
returns points into the arena.

## Limits and statuses

A result-producing guest export returns:

| | |
|---|---|
| `>= 16` | success, the number of bytes used, including the transfer header |
| `-1` | the output region could not hold the 16-byte transfer header |
| `-2` | the region could not hold the whole tree |

`iterator_next` has its own protocol, since a yielded `None` has to stay
distinct from exhaustion: `1` for an item, `0` for exhaustion, `-1` for an
exception it has written out, and `-2` for either a bad region or an overflow.

A Python exception is **not** a negative status. It comes back as a
`KIND_EXCEPTION` value with a positive length, which keeps a Python-level
failure distinct from an ABI one.

Depth is bounded at 128 on both sides (`MP_VALUE_MAX_DEPTH` in C,
`maxDecodeDepth` in Go). They must stay equal, and Go must not be the tighter of
the two, or a tree C is willing to write is one Go will refuse to read.

The 16KB result region is a default transfer size, not a semantic maximum, but
today it is a hard ceiling: a value larger than it fails with `ErrArenaFull`.
Retrying against a larger region is not implemented, and a blind retry would be
wrong, since re-running `eval` re-runs the Python. It needs the guest to hold
the computed result and re-serialize on request.

## Where things live

The C side is one file per subsystem, named the way MicroPython's own ports name
them, with every guest export in `main.c` and nothing else in it.

```
build/abi.h           the wire format alone: mp_value_t, the kinds, the word packing
build/arena.h/.c      mp_arena_t, the bump allocator, the output-arena lifecycle
build/refs.h/.c       the pin list, and the imports the host counts through
build/value.h         the conversion API
build/encode.c        value_from_obj: guest objects out
build/decode.c        obj_from_value: host values in
build/hostfn.h/.c     the host callback object and its two stack arenas
build/pymodule.h/.c   the module tree a host package is published into
build/exec.h/.c       compile and run one fragment of source
build/gccollect.h/.c  the C stack: what the collector scans, and scrub_dead_stack
build/vm.h/.c         heap and interpreter startup
build/mphalport.c     the hooks MicroPython expects a port to supply
build/main.c          every guest export, and nothing else

internal/host/memory/ linear memory, and Arena
internal/host/codec/  the ABI in Go: Value, EncodeInto, Consume
internal/host/        Module: one interpreter, its arena, its references
internal/value/       the semantic value model, and Lower
internal/api/         serialisation of access, snapshots, cancellation
```

The conversion is split by direction rather than by type, so `build/encode.c` and
`build/decode.c` pair with `internal/host/codec/encode.go` and `decode.go`: the
two halves of one kind live in the two files that face each other.

`internal/host/abi/abi.go` is generated from `build/abi.h` by `cgo -godefs`, so
the kind constants and transfer structure sizes cannot drift between the two
sides. `abi.h` includes no MicroPython headers, which is what lets that
generation run against a plain host compiler with only `build/` on the include
path.

## Known limitations

**Reference release runs on Go's schedule, not the guest's.** A handle is only
queued once the Go collector finds it, so a loop that mints handles while
allocating almost nothing on the Go side may never trigger a collection. The
guest then fills up with pinned objects while the releases that would free them
sit unqueued, and it cannot ask Go to collect. It shows up as a guest
`MemoryError` in a loop that looks like it should be flat, and only for
expressions producing a fresh uncopyable object each time: a new closure or
class instance, not `Eval("f")` on the same function, which reuses one slot.

This is deferred release, not lost memory, and it is not something `eval` does
on its own. The same 500 iterations run entirely inside the guest cost 256
bytes, and 500 host `Eval("1")` calls, which compile exactly as often but mint
no handle, cost nothing at all.

Most of what a stalled loop holds is just what the objects weigh. Keeping 500
functions alive costs 24,064 bytes in plain MicroPython, 48 bytes each, whether
a Python list or the host is what holds them. That is the size of a function
object, not compilation churn: a freshly compiled lambda and a closure over
already-compiled code cost the same 48 bytes. Measured for 500 evals of
`lambda: 1`:

| when the Go collector runs | guest heap still held afterwards |
|---|---|
| never | 26,032 bytes, of which 24,064 is the lambdas themselves |
| once, at the end | 2,016 bytes, all of it reference table |
| every iteration | 0 bytes |

So the host's own overhead is the 2,016, and the rest is the ordinary price of
keeping 500 objects reachable. `Eval("f")` 500 times against one global function
costs 16 bytes, since `ref_add` reuses the slot. Given a 64KB heap the
uncollected form raises `MemoryError` after roughly 400 iterations, though that
figure is a fragmentation threshold rather than a capacity, so it moves with
allocation order.

Long guest calls can also accumulate queued releases: no operation boundary
occurs while they run. A future explicit release or scoped-handle API would need
to define how copies share ownership, which is tractable because every `Value`,
`Object` and `Func` aliases one `*Ref` rather than owning a share of it.

**The reference table never shrinks.** `host_refs` grows to the high-water mark
of concurrently live handles and stays there: releasing a slot nulls the entry
but never truncates the list. At about 4 bytes per peak slot that is the 2,016
bytes above, against 0 when handles are released promptly and 480 with 100 live
at a time. It is bounded by peak concurrency rather than by total handles
minted, but it is not returned. The host-side tables reuse slots through a free
list, so they track the same high-water mark in Go memory, which is not the
constrained resource.

A second-order effect matters more than the bytes: the list doubles when it
grows, which asks for one large contiguous block from a heap that the pinned
objects themselves have fragmented. A `MemoryError` in this situation is usually
that request failing rather than the heap being full. Moving `host_refs` to a
plain array outside the GC heap, scanned through `gc_collect_root`, would remove
the last of this from Python's allocator.

**A result larger than the transfer region fails rather than retrying.** See
Limits and statuses above. The error wraps `ErrArenaFull`, but that lives in
`internal/host/memory`, so a caller outside this module cannot match it with
`errors.Is` and has only the message to go on: `transfer arena full: 16384 byte
result region`.

## Notes for anyone changing this

Finish each guest-to-host transfer before resetting its arena. Object metadata
is arena-owned, but unclaimed references still require `value_release_refs`.

MicroPython's collector scans the C stack conservatively, so a pointer left in a
finished call's frame can keep a dead object alive for one collection. Releasing
a reference clears a 4KB window of dead stack for that reason
(`scrub_dead_stack`). `release_transfer` also scrubs after releasing unclaimed
references or receiving an exception, whose serialization may already have
rolled back its references. Successful scalar transfers do not scrub.

The wasm build is reproducible. `WASI_SDK=... BINARYEN=... ./build/build.sh`
regenerates `internal/micropython/` and gives byte-identical output for
unchanged sources, so a rebuild that changes the artifact means a source change
really did land.
