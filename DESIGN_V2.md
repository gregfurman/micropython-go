# Guest–host design

This document describes the current component internals of `micropython-go`,
with emphasis on how Go and MicroPython exchange values and share responsibility
for their lifetimes. It describes the source implementation, including the split
C bridge in `build/`; it is not a proposal to adopt the WebAssembly port's proxy
design. It complements the earlier [DESIGN.md](DESIGN.md) and the detailed
[ARCHITECTURE.md](ARCHITECTURE.md).

The central distinction is between **temporary transfer bytes** and **persistent
Python objects**. Arenas own the bytes used during an exchange. References keep
Python objects alive after that exchange ends. Reclaiming an arena does not
release references that Go has already claimed.

## 1. Components and execution model

MicroPython and the C bridge are compiled to WebAssembly, then translated into
Go. At runtime, both host and guest execute inside the Go process; there is no
separate guest process or runtime WebAssembly engine. “Guest” still identifies
the translated interpreter, its linear address space, and its Python heap.

```text
Go application
    |
Public Instance / Program / Value
    |
internal/api.Instance       serializes access, cancellation, trap state
    |
internal/host.Module        coordinates calls, arenas, references, callbacks
    |        |        |
    |        |        +-- OwnedReferences: slots, counts, cleanup queue
    |        +----------- Codec: semantic values <-> ABI records
    +-------------------- Memory: Go-backed guest linear memory
    |
Generated guest Module     Xeval, Xcall, Xcall_ref, ...
    |
C bridge + MicroPython     Python execution, object conversion, GC roots
    |
    +---- imports back into host: callbacks, reference bookkeeping,
                                 stdout, cancellation polling
```

| Component | Responsibility | Main source |
|---|---|---|
| Public API | Stateful interpreters, pooled programs, typed values | [instance.go](instance.go), [program.go](program.go), [value.go](value.go) |
| Instance controller | One operation at a time; lifecycle and trap recovery | [internal/api/instance.go](internal/api/instance.go) |
| Host coordinator | Reserve buffers, invoke guest exports, consume results | [internal/host/module.go](internal/host/module.go) |
| Value model | Convert arbitrary Go values to a closed set of semantic types | [internal/value](internal/value) |
| Codec and ABI | Encode/decode records; share layout constants with C | [internal/host/codec](internal/host/codec), [build/abi.h](build/abi.h) |
| Memory and arenas | Linear memory backing, allocation, temporary storage | [internal/host/memory](internal/host/memory), [build/arena.c](build/arena.c) |
| Reference ownership | Host bookkeeping and guest GC pins | [internal/host/refs.go](internal/host/refs.go), [build/refs.c](build/refs.c) |
| Guest bridge | Exports, conversion, callbacks, module installation | [build/main.c](build/main.c), [build/encode.c](build/encode.c), [build/decode.c](build/decode.c), [build/hostfn.c](build/hostfn.c), [build/pymodule.c](build/pymodule.c) |
| Interpreter support | Startup, stack scanning, port hooks, source execution | [build/vm.c](build/vm.c), [build/gccollect.c](build/gccollect.c), [build/mphalport.c](build/mphalport.c), [build/exec.c](build/exec.c) |

`internal/api.Instance` holds a channel-based lock for the entire operation,
including callbacks into Go. Different instances can execute independently.
`host.Module` assumes this serialization; its methods are not independently
safe for concurrent guest execution.

A guest callback runs synchronously inside the original operation. It must not
call the same instance's public execution methods and wait for them: those
methods need the lock the outer operation still holds. Reference-bookkeeping
imports deliberately bypass that lock.

## 2. Memory domains and arenas

The host owns the backing `[]byte` for guest linear memory. ABI pointers are
32-bit offsets into that memory, not Go pointers or native process addresses.
The generated module holds a pointer to the slice so memory growth can replace
its backing allocation.

There are three distinct allocation domains:

| Domain | Contents | Reclamation |
|---|---|---|
| Go heap | Decoded values, callback functions, reference bookkeeping, linear-memory backing | Go GC |
| Guest libc allocation space | Python heap backing, owning arena spans, arena spills | Guest `malloc`/`free`; ultimately the backing memory becomes Go garbage when unreachable |
| MicroPython GC heap | Python objects, container snapshots, reference root list | MicroPython mark-and-sweep GC |

The module's transfer arena uses the guest **libc allocator**, not individual
MicroPython GC allocations. The Python heap itself is a separate libc allocation
made during VM initialization. Increasing the Python heap size does not increase
the per-result transfer capacity.

The default Python heap is 128 KiB. The linked guest starts with 384 KiB of
linear memory, including a 192 KiB reserved stack; allocations can grow linear
memory in 64 KiB pages. MicroPython's configured C-stack check limit is 96 KiB.
These sizes describe different budgets, not interchangeable heap limits.

### Arena ownership

An arena is a four-byte-aligned bump allocator: each reservation advances an
offset. Individual payloads are not freed separately.

| Arena | Owner and lifetime | Capacity and overflow |
|---|---|---|
| Host module arena | One owning arena per module; operations temporarily reserve from it | 64 KiB base span; reservations that do not fit use separately tracked libc allocations |
| Guest result arena | C view of an output region reserved by Go for one export | Currently 16 KiB, including the transfer header; cannot spill |
| Callback argument arena | Guest stack storage until the callback completes | 16 KiB of payloads; top-level argument records are a separate stack array |
| Callback return arena | Guest stack storage lent to Go until C consumes the return | 16 KiB, including a root value record; Go uses a non-owning `ArenaAt` view and cannot spill |

Host operations use this lifetime pattern:

```go
defer i.arena.Mark()()
```

`Mark` captures the current offset and spill count. Its deferred reset frees
later spills and rewinds the offset on success or error. The base span remains
allocated for reuse. Marks rely on single-use, last-in-first-out discipline.

The owning arena can satisfy large input allocations by spilling, but this does
not enlarge the 16 KiB region passed to a guest result writer. A guest result
remains one contiguous transfer. Host inputs can span multiple allocations,
all owned by the same arena scope.

Decoding copies strings, bytes, descriptions, and container contents into Go
storage. No returned value aliases transfer memory. In the other direction,
the guest constructs Python objects from the input bytes before the host resets
the arena. An object handle resolves to an existing Python object instead of
copying it.

## 3. Application binary interface

The application binary interface (ABI) is an internal contract between the C
bridge and the Go host. [build/abi.h](build/abi.h) defines the wire format;
`internal/host/abi/abi.go` contains generated constants and structure sizes.
There is no runtime ABI-version negotiation: both halves must be rebuilt
together when the contract changes.

### Value record

Every value starts with a 12-byte, four-byte-aligned record. Words are encoded
little-endian; a 64-bit scalar occupies the two payload words, low word first.

```c
typedef struct {
    uint32_t kind;
    uint32_t w1;
    uint32_t w2;
} mp_value_t;
```

| Kind | `w1` | `w2` |
|---|---|---|
| `INVALID` | Unwritten/invalid record sentinel | — |
| `NULL`, `NONE` | No payload | No payload |
| `BOOL` | 0 or 1 | 0 |
| `INT`, `FLOAT` | Low 32 bits | High 32 bits |
| `STR`, `BYTES`, `BIGINT`, `EXCEPTION` | Byte length | Payload pointer |
| `LIST`, `TUPLE`, `SET`, `FROZENSET` | Element count | Pointer to consecutive value records |
| `DICT` | Pair count | Pointer to alternating key/value records |
| `OBJECT`, guest to host | Reference ID | Object-info pointer with attribute flags in its low two bits |
| `OBJECT` or `REF`, host to guest | Existing reference ID | Unused |

`NONE` represents Python `None`; `NULL` represents MicroPython's internal null
sentinel and is an error when consumed by Go. `REF` is accepted on guest input
but rejected on host input; the Go encoder currently sends handles as `OBJECT`.
Empty byte payloads use a zero length and null pointer. Empty container pointers
need not be dereferenced and should not be assumed to have a canonical value.

`BIGINT` carries decimal text. Go can send it, but the current C serializer sends
Python integers outside `int64` range as `OBJECT` handles, not `BIGINT` records.

Ordinary builtin containers are recursively copied. This preserves values and
container kinds, not shared container identity. A repeated container outside the
active recursion path can be copied twice. Cycles and containers at the guest
depth limit become handles instead. `Resolve` reserializes a referenced object
using the same rules; it is not a shallow, exactly-one-level read.

The current serializer snapshots list elements and dict/set entries into guest
tuples before recursively converting children. Child conversion can invoke
Python `__repr__`, mutate the original collection, or run GC. The snapshots
stabilize each visited collection's membership and root its children; they do
not make the whole object graph an atomic snapshot. Immutable tuples are read
directly.

### Transfer and object metadata

A result starts with a 16-byte transfer header: the root `mp_value_t` followed by
the head pointer of a reference-acquisition list. Other records and payloads
follow in the same output region.

Each guest-to-host object has an arena-owned sidecar:

```c
typedef struct {
    uint32_t len;
    uint32_t ref;
    uint32_t next;
    char blob[];
} mp_object_info_t;
```

`blob` contains `type + '\x04' + repr`. `ref` identifies the acquisition still
owned by this transfer; Go clears it when claiming ownership. `next` links the
sidecars for cleanup independently of the value tree. The pointer's low bits
carry callable and iterator-related flags. Although the flag is named
`ITERABLE`, the current detection primarily identifies iterators, not every
object Python can iterate over.

Callback arguments use the same sidecars, but their list head is a C local, not
a transfer header. Callback returns and host input records carry existing
handles and do not acquire new references.

### Calls and statuses

The main host-to-guest operations are `eval`, `exec`, named `call`, `call_ref`,
`get_global`, `set_global`, `ref_to_value`, and `iterator_next`. Module-installation
exports create Python modules and attach Go functions or values. Lifecycle and
cleanup exports include `init_vm`, `reset_refs`, `release_ref`, and
`release_transfer`; libc also exposes `malloc` and `free`.

Most result-producing exports take `(out_ptr, out_capacity)` and return:

| Result | Meaning |
|---|---|
| `>= 16` | Bytes used, including the transfer header; the root may be an exception |
| `-1` | Output capacity cannot hold the transfer header |
| `-2` | Serialization overflowed the output arena |

`iterator_next` instead returns `1` for an item, `0` for exhaustion, `-1` for a
serialized Python exception, and `-2` for inadequate output capacity. This keeps
a yielded `None` distinct from exhaustion.

The application-level guest imports are:

| Import | Host behavior |
|---|---|
| `host_trampoline(id, args, count, out, capacity)` | Dispatch a registered Go callback and encode its return |
| `go_ref_add(addr)` | Acquire a reference slot for a guest object address |
| `go_ref_free(id)` | Release an acquisition; report whether C should remove its pin |
| `host_stdout(ptr, len)` | Forward guest bytes to the configured writer |
| `host_poll()` | Read the atomic cancellation flag |

These sit alongside imported linear memory and generated runtime support; they
are not an exhaustive list of compiler/runtime linkage requirements.

## 4. A host-to-guest operation

For a call with arguments and a result:

1. The API acquires the instance lock and checks closed/trapped state.
2. `Module.Begin` clears the cancellation flag and drains queued reference
   releases before argument handles are reduced to IDs.
3. The module marks its arena, reserves names, argument records and payloads,
   and a 16 KiB result region. `value.Lower` normalizes Go inputs; the codec
   writes the ABI tree and validates the owner of each handle.
4. The guest decodes inputs into Python objects, executes the operation, and
   serializes the result under a non-local-return (`nlr`) exception handler.
5. Go checks the returned status and consumes the root, copying data and
   claiming reference acquisitions.
6. Deferred `release_transfer` releases unclaimed acquisitions. Only then does
   the arena mark rewind its allocations and the API release its lock.

This cleanup order matters: the reference list is stored inside the arena and
must be consumed before its memory can be reused.

## 5. Registering and invoking Go callbacks

Callback registration is separate from Python-object reference registration.
`Module.registry` holds Go `HostFunc` closures under incrementing integer IDs.
The guest stores a bound MicroPython callable containing that ID. Python never
receives a Go function pointer.

`DefineFunction` installs the callable in globals; package registration installs
it in a module dictionary. The registry keeps the Go closure reachable until
the registry is replaced or its module becomes unreachable. There is no
per-function unregister protocol tied to Python GC.

When Python invokes the callable:

1. C serializes arguments into stack records and the argument arena, recording
   any reference acquisitions in its local list.
2. `host_trampoline` enters Go without releasing or reacquiring the instance
   lock. The dispatcher checks the callback ID and argument count, then consumes
   each argument. A callback may retain the resulting Go values.
3. The Go function executes with a context tied to the module's cancellation
   signal. This is not the original caller context with its values/deadline.
4. Go reserves the 12-byte root in the borrowed return arena and encodes the
   result. Callback errors and panics become exception records; if their text
   does not fit, the host writes a payload-free exception.
5. C releases unclaimed argument references, decodes the return into a Python
   object, and raises it if its root kind is `EXCEPTION`.

The C exception handler also releases argument acquisitions when serialization
fails before the trampoline runs. Current callbacks accept at most eight
positional arguments. Both stack arenas disappear when the guest callback frame
returns; retained Go values survive because their data was copied and their
handles claimed.

## 6. Registering and releasing Python references

MicroPython's collector cannot trace Go objects. A Go handle or an address in a
Go map does not keep a Python object alive. The guest therefore owns one
`MP_REGISTER_ROOT_POINTER` list, `host_refs`, containing the actual Python
objects pinned on behalf of the host. Slot zero is reserved.

`OwnedReferences` keeps the remaining bookkeeping in Go:

- `slots`: object address, generation, and outstanding acquisition count.
- `byAddr`: address-to-slot index for deduplication.
- `free`: reusable slot indices.
- `pending`: IDs queued by asynchronous Go cleanups.

MicroPython's non-moving collector makes address-based deduplication possible.
An object can have many acquisitions but occupies one guest root slot.

### Acquisition and transfer

```text
Guest object
    -> ref_add -> Go Acquire -> slot/count -> guest root pin
    -> transfer sidecar owns acquisition
    -> Go Track + clear sidecar.ref
    -> *value.Ref owns acquisition
    -> Go cleanup queues ID
    -> next Begin drains queue -> last release removes guest pin
```

More precisely, `ref_add` first calls `go_ref_add`. Go reuses an address mapping
and increments its count, or allocates a slot with count one. C then ensures the
root list contains the object at that slot. If growing the list raises, C gives
the acquisition back. Once acquisition succeeds, the serializer immediately
links its sidecar, with no potentially raising operation in between.

The decoder verifies that the sidecar owns the ID in the value record, creates
a `*value.Ref` through `Track`, then zeros the sidecar's `ref` field. `Track`
does not increment the count: it adopts an already-counted acquisition. Each
newly decoded object record gets a separate Go handle. Copies of that Go object
value share its handle.

The sidecar list makes partial failure manageable. Cleanup does not need to
walk a partially written value tree. It releases every nonzero sidecar ID;
already-claimed handles remain owned by Go, including handles created before a
later decoding error.

### Final release and validation

`runtime.AddCleanup` queues an ID after its Go handle becomes unreachable. It
does not call the guest. `Drain` detaches the queue under the reference mutex,
then releases each acquisition. A last release removes the address mapping,
advances the generation, and frees the slot. The host then invokes `release_ref`
to remove the guest pin. Guest-side transfer cleanup instead calls `go_ref_free`
and removes the pin if the host reports the last release.

No guest execution may intervene between a final host release and removal of
its pin. The instance lock and synchronous imports provide that ordering.
Cleanup goroutines only enqueue; they never change slots or guest memory.

IDs pack a 20-bit slot index and 12-bit generation. A Go `Ref` additionally holds
its `OwnedReferences` owner identity. `Lookup` checks owner, live count, and
generation before sending an ID back to Python. The C lookup only checks the
slot and its pin; it relies on host validation for generation checks.

Removal of a pin makes an object eligible for guest collection, not necessarily
immediately collectible. Python may still reference it. Conservative stack
scanning can also retain stale pointers. The bridge scrubs a bounded dead-stack
region on final host releases and selected transfer-cleanup paths to reduce
that retention; this is not a guarantee of immediate reclamation.

Neither `Begin` nor snapshot creation runs either collector. Release latency
depends first on Go cleanup scheduling, then on a drain point, then on guest GC.
A small Python heap can therefore fill with otherwise-unused pinned objects
before Go decides to collect their handles.

## 7. Exceptions, cancellation, and traps

Ordinary Python exceptions cross as `EXCEPTION` values, with a blob containing
`type + '\x04' + message + '\x04' + traceback`. The Go codec converts this to
an error. Host-to-guest exceptions contain type and message; C recreates a
recognized builtin exception class or defaults to `HostError`, a `RuntimeError`
subclass.

When result serialization raises, C releases partial acquisitions and resets
the output arena before writing an exception. The overflow flag stays set, so
a smaller error record does not hide that the original result failed to fit.
Exception formatting and its final arena copy run under their own handler,
with a non-raising fallback.

The translated `setjmp`/`longjmp` support uses a distinguished Go panic to
implement guest unwinding and restore the guest stack pointer. Other panics
escaping a normal API operation become `TrapError` and mark the instance as
trapped. Subsequent operations require reset/restore; an ordinary Python error
does not poison the instance.

Cancellation is cooperative. A context cancellation or explicit `Cancel` sets
an atomic flag; VM loop/return hooks poll it on a 256-hook countdown and raise
`KeyboardInterrupt`. Provided sleep functions also poll. Go callbacks must
cooperate with their cancellation context; cancellation cannot forcibly stop an
arbitrary blocking Go function.

The callback cancellation signaller is currently one-shot. `Begin` clears the
guest polling flag but does not reset that signaller, so later callback contexts
on a cancelled module remain cancelled. Treat this as a current limitation, not
as per-operation context isolation.

## 8. Snapshots and interpreter lifetimes

A snapshot contains a copy of linear memory, the guest stack pointer, the module
arena position, the callback registry and ID counter, and the stdout writer.
Snapshot creation holds the instance lock and drains pending releases first.
It is an idle-state snapshot, not a suspended-call continuation.

Restore loads the memory image and arena state, resets the guest reference root
list, and replaces `OwnedReferences` and the codec. Handles from before the
restore fail owner validation, even if a new object receives the same numeric
ID. Old cleanup callbacks retain only the old owner and cannot release pins in
the restored interpreter. Arena spill bookkeeping from the abandoned timeline
is discarded without freeing addresses against the restored allocator.

The callback registry is restored because guest callables contain registry IDs.
Its Go closures are shared references, not deep snapshots: mutable state captured
by a closure is not rolled back with Python memory.

`Program` pools instances initialized from a baseline snapshot and restores
them after calls. Copied results remain usable; live handles belong to the
abandoned execution state and cannot be used after that rewind. Use a stateful
instance when object identity must span operations.

`Close` cancels execution, waits for the instance lock, and drops the runtime
pointer. It does not individually free all guest allocations or run guest
finalizers. The Go backing memory is reclaimed when no remaining Go references
retain the module.

## 9. Constraints and hardening boundaries

The design is a private ABI between matching components, not a validated format
for arbitrary binary input. The distinction matters when changing serializers
or treating memory corruption as recoverable.

- Result and callback regions are fixed at 16 KiB today. `ErrArenaFull` is
  distinct from guest allocation failure. Automatically retrying the Python
  operation could repeat side effects; a safe larger-buffer retry would need
  a protocol that retains and reserializes the already-computed result.
- Guest copy depth and Go decode depth are 128. Go input lowering/encoding has
  a separate depth limit of 32. These are different directional constraints.
- Host reads are checked against linear memory, not the individual transfer
  region. `consumeArena` does not reject a reported size above capacity, and
  container decoding allocates its Go slice before validating every record.
  C reference-list cleanup also trusts sidecar pointers and links.
- `ArenaAt` and `Arena.View` do not themselves enforce arena-local bounds or
  alignment. Marks do not enforce LIFO/single-use discipline, and arena snapshot
  state does not include spill allocations. Callers must preserve these rules.
- The memory abstraction advertises a wasm32 maximum of 4 GiB, but host view
  APIs reject negative `int32` offsets. This is not a fully usable 4 GiB ABI.
- Generation checks are bounded and eventually wrap. There is also a current
  signedness mismatch: `Acquire` returns an `int32` packed ID, while C treats
  any negative result as failure. Generations setting bit 31 cannot complete
  registration correctly. Acquisition counts are `uint32` with no overflow
  guard. Neither limit should be described as unbounded protection.

These constraints do not change the intended ownership model. They identify
where correctness still depends on matched code, disciplined callers, and
bounded workloads rather than explicit validation.

## 10. Build and maintenance contract

[build/build.sh](build/build.sh) generates libc support and MicroPython headers,
compiles the bridge and interpreter with the WASI SDK, runs Binaryen with
`--spill-pointers`, and translates the result with `wasm2go`. Pointer spilling
supports conservative guest stack scanning. Generated Go code and embedded data
live under `internal/micropython` alongside provided unwinding and clock support.

ABI constants are generated separately through
[internal/host/abi/package.go](internal/host/abi/package.go). C static assertions
check record sizes and alignment. These checks reduce layout drift but do not
automatically regenerate stale artifacts or verify all semantic conventions.

When changing the boundary, keep the following pairs aligned:

- Guest `encode.c` with host `codec/decode.go`.
- Host `codec/encode.go` with guest `decode.c`.
- `abi.h` with generated Go ABI constants and the rebuilt guest.
- Guest reference pins with host slot/count transitions and transfer cleanup.
- Callback import signatures with host dispatch and translated invoke support.

Relevant regression coverage lives in
[transfer_test.go](internal/host/transfer_test.go) for abandoned/partial transfers,
[arena_test.go](internal/host/memory/arena_test.go) for allocation lifetimes,
[instance_test.go](internal/api/instance_test.go) for reference validity and
restore isolation, and [memory_test.go](memory_test.go) for delayed reclamation.
This document is based on source inspection; it does not certify that generated
artifacts include every current C source change.
