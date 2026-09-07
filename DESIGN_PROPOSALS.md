# Guest-host simplification proposals

Status: analysis and proposals, not implemented. Reviewed against the working
tree on September 7, 2026. [DESIGN_V2.md](DESIGN_V2.md) describes the current
implementation; [DESIGN.md](DESIGN.md) records the earlier arena-based design.

## 1. Recommendation

Keep the small typed value ABI and a reusable, spillable host arena. Reconsider
the requirement that both directions serialize a complete recursive value tree.

The most promising structural change is:

```text
                         Go value model
                         /            \
                        /              \
              Go -> Python          Python -> Go
                   |                     |
            encode input tree     inspect rooted result
                   |                     |
            small host arena      shallow/batched records
                   |                     |
            C builds objects      Go materializes values
                   |                     |
             Python objects         Go-owned values

                    Persistent opaque identities
                               |
                     guest roots + Go handles
```

This deliberately gives up ABI symmetry while retaining eager Go values at the
public boundary. It does **not** require turning every public `Value` into a
lazy proxy or removing the guest reference table.

The arena allocator is not the main source of complexity. The difficult part is
combining recursive result serialization, eager object descriptions, arbitrary
Python execution during conversion, fixed output capacity, and reference
acquisitions that can escape a partially completed transfer.

Priorities:

1. Fix allocator initialization and measure smaller scratch storage independently
   of changing the ABI.
2. Prototype shallow, batched output conversion while retaining the existing
   input encoder and reference-counting model.
3. Remove eager `repr` from the transport, with an explicit compatibility decision
   about object descriptions.
4. Consider shared weakly cached handles only after the transfer model is stable.

Performance preservation is an acceptance criterion, not a result established
by this review. The existing fast scalar path should be the baseline to beat.

## 2. Evidence and reference implementations

The comparison uses these local sources:

| Source | Relevant pattern |
|---|---|
| [Current host module](internal/host/module.go), [codec](internal/host/codec), [guest encoder](build/encode.c), [guest decoder](build/decode.c) | Complete copied input/output trees, arena scopes, opaque references |
| [SQLite allocation wrapper](../go-sqlite3/internal/sqlite3_wrap/alloc.go) | 4 KiB reusable arena with spills; stack marks for small scratch records; explicit heap ownership |
| [SQLite guest entry point](../go-sqlite3-wasm/build/main.c) | Thin composition layer; explicit allocator initialization before library initialization |
| [SQLite text bridge](../go-sqlite3-wasm/build/text.c), [statement bridge](../go-sqlite3-wasm/build/stmt.c) | Destructor-based input ownership; batched output descriptors pointing to library-owned data |
| [SQLite Go statements](../go-sqlite3/stmt.go), [callbacks](../go-sqlite3/func.go) | Copied versus borrowed access; borrowed callback arguments |
| [MicroPython proxy C](micropython/ports/webassembly/proxy_c.c), [proxy JS](micropython/ports/webassembly/proxy_js.js) | Shallow conversion, guest roots, weak proxy reuse, finalization |
| [PyProxy conversion](micropython/ports/webassembly/objpyproxy.js), [WebAssembly GC support](micropython/ports/webassembly/main.c) | Separate deep conversion; raw container views; variant-dependent collection strategy |

Benchmarks below exercised the existing generated guest, without rebuilding it.
Source findings describe the current C files. The review does not establish
that every current C edit is present in the generated artifact.

## 3. Current encode/decode flows

### 3.1 Go input to Python

For `Call("echo", value)`:

```text
HOST / Go                                      GUEST / linear memory

public Value or any
        |
        v
value.Lower
        |  normalize numeric/container types
        v
internal/value.Value
        |
        v
Codec.EncodeInto -----------------------------> module arena
        |                                      +-----------------------+
        |                                      | argument Value[12]    |
        |                                      | child Value records   |
        |                                      | string/byte payloads  |
        |                                      +-----------+-----------+
        |                                                  |
        |                         obj_from_value() <--------+
        |                                  |
        |                                  v
        |                           MicroPython GC heap
        |                           new Python objects
        |                                  |
        |                                  v
        |                           call Python function
        |
after result consumption:
arena Mark reset -----------------------------> rewind bytes; free spills
```

The arena's storage comes from guest libc `malloc`, not MicroPython's GC
allocator. Input strings/bytes are copied into that storage and then copied
again into Python-owned objects. Containers have both a wire representation and
a newly constructed Python representation. C builds a temporary list for
sequence inputs, converting it to tuple/set/frozenset where needed.

Handles are the exception: the Go encoder validates owner and generation, and
C resolves the ID to the original Python object. Sending an existing handle does
not acquire a new persistent reference.

This direction has a useful property: failure cleanup is mostly one arena
reset, while incomplete Python objects remain the guest collector's job. It is
a reasonable part of the architecture to retain.

### 3.2 Python result to Go

```text
HOST                                             GUEST

reserve 16 KiB --------------------------------> output_arena_init
                                                       |
                                                 run Python
                                                       |
                                                       v
                                               result object graph
                                                       |
                                               value_from_obj
                                               /      |       \
                                              /       |        \
                                        copy bytes  recurse   opaque object
                                                     |          |
                                             snapshot mutable  print repr
                                             container entries  acquire ref
                                                     |          |
                                                     +----+-----+
                                                          |
                                             serialized tree + ref list
                                                          |
Codec.Consume <--------------------------------------------+
    |
    +-- copy bytes/strings to Go
    +-- recursively allocate Go containers
    +-- Track opaque IDs; clear their sidecar ownership fields
    |
release_transfer ------------------------------> free unclaimed acquisitions
    |
arena reset -----------------------------------> reuse output region
```

For an illustrative result `[1, "abc", fn]`, the output contains:

```text
out + 0     root Value: LIST, count=3, ptr=out+16
out + 12    reference-list head ------------------------+
out + 16    Value: INT                                  |
out + 28    Value: STR, len=3, ptr=out+52                 |
out + 40    Value: OBJECT, id, info_ptr|flags             |
out + 52    "abc"                                       |
out + 55    alignment padding                           |
out + 56    object info <-------------------------------+
                len
                ref=id       becomes 0 when Go claims it
                next=0
                "type\x04repr"
```

This is an illustrative layout, not captured memory. Objects normally have a
separate sidecar because their two payload words already hold an ID and a
pointer with attribute bits.

The string path copies payload bytes twice after Python produces them:

```text
Python string bytes --copy--> output arena --copy--> Go string
```

The container path creates another complete tree of wire records. A list of
1,000 inline integers requires `16 + 1,000 * 12 = 12,016` output bytes, in
addition to the Python list, its temporary membership snapshot, and the decoded
Go collection. This excludes allocator overhead and alignment outside the
transfer. No guest reference is needed for those copied integers.

Cycle/depth fallback avoids infinitely recursive copies by returning handles.
It does not preserve arbitrary shared container identity: repeated, non-cyclic
subtrees can still be copied more than once.

### 3.3 Python calls a Go callback

Callbacks reverse the memory ownership but reuse both recursive conversions:

```text
Python arguments
      |
      v
C stack frame
  +---------------------------------------------------+
  | argument records: up to 8 * 12 bytes               |
  | argument payload arena: 16 KiB                    |
  | argument acquisition-list head                    |
  | return arena: 16 KiB                              |
  +---------------------------------------------------+
      |                                      ^
      | host_trampoline                      |
      v                                      |
Go consumes arguments -> runs callback -> encodes return
      |                                      |
      | copied values may escape             | borrowed arena view
      | claimed handles may escape           | cannot spill
      v                                      v
C releases unclaimed argument refs -> decodes return -> Python value
```

Even a scalar callback reserves the two large payload buffers. They consume
about 32 KiB of guest stack, not 32 KiB of newly allocated Go heap on each
invocation. The generated callback function also has stack-frame overhead.

Go resets a failed callback return by taking a fresh view of the return region.
If the error text will not fit, it writes a payload-free exception. C must keep
its argument cleanup scope active across serialization and the trampoline.

### 3.4 Two ownership clocks

```text
transfer bytes:   allocate -------- consume -------- reset
                                    |
opaque object:   acquire ------------+-- Go handle -------------------+
                                                                    |
                                                  Go GC finds handle dead
                                                                    |
                                                  cleanup queues reference
                                                                    |
                                                  next Begin drains queue
                                                                    |
                                                  remove last guest pin
                                                                    |
                                                  later guest GC can collect
```

The guest root list is essential: Go's reference table cannot be traced by
MicroPython. The host owns address deduplication, slot generations, counts, and
cleanup queues. Each serialized occurrence acquires a count; each decoded
occurrence creates a new Go handle, even for the same guest object.

The linked transfer list is not accidental complexity. Under the current
fallible tree protocol, it accounts for acquisitions in subtrees that Go never
visits. Removing the list alone would leak pins on partial failure.

## 4. Memory sizes: budgets, actual residency, and copies

### 4.1 Current configured budgets

| Budget | Current size | Meaning |
|---|---:|---|
| Initial linear memory | 384 KiB | Linked data, stack reservation, and remaining initial address space |
| Reserved guest stack | 192 KiB | Part of initial linear memory, not an additional allocation |
| MicroPython C-stack check limit | 96 KiB | Checked execution budget, distinct from the linked reservation |
| Default Python GC heap | 128 KiB | A libc allocation; excludes transfer arena storage |
| Module arena | 64 KiB | Persistent libc allocation with operation-scoped spills |
| Normal result region | 16 KiB | Reservation inside that arena; not another persistent allocation |
| Callback payload regions | 2 * 16 KiB | Guest stack usage while callback frame is active |
| Linear-memory growth quantum | 64 KiB | WebAssembly pages |

Do not sum this table: several rows are subdivisions of other rows.

The current 16 KiB result cap admits at most 16,368 bytes of a single root
string/bytes payload. A flat integer list fits at most 1,364 element records;
a flat dict with inline keys and values fits at most 682 pairs. Real mixed
values fit fewer entries. Larger guest heaps do not increase these limits.

The module arena's spill support helps large **inputs**. It does not let C spill
past the separately bounded output region. A blind retry of `eval` or `call`
would repeat Python side effects, so output growth needs retained-result
reserialization, not operation replay.

### 4.2 Measured startup sizing

A temporary Go test overlay constructed the current generated module, called
`init_vm(128 KiB, 8)`, and allocated alternative arena spans. It did not change
repository implementation files.

| Arena span | Linear memory after VM init | Linear memory after arena | Go backing slice capacity | Snapshot image bytes |
|---|---:|---:|---:|---:|
| 4 KiB | 576 KiB | 576 KiB | 608 KiB | 576 KiB |
| 16 KiB | 576 KiB | 576 KiB | 608 KiB | 576 KiB |
| 32 KiB | 576 KiB | 576 KiB | 608 KiB | 576 KiB |
| 64 KiB, current | 576 KiB | 640 KiB | 768 KiB | 640 KiB |

For this build, the 64 KiB arena crosses an allocator/page boundary. A 32 KiB
arena saves one logical page and 160 KiB of backing-slice capacity at startup.
These are observed sizes, not promises for every compiler/build configuration.

An arena-only shrink to 4 KiB is not a complete optimization: every current
16 KiB output reservation would spill. Even a 16 KiB base can spill its output
after the function name or input records consume a few bytes. Benchmark 32 KiB
as an incremental change; reconsider 4 KiB only after separating/removing the
large output reservation.

### 4.3 Allocator initialization differs from SQLite

SQLite's `main.c` calls `init_allocator()` before `sqlite3_initialize()`. That
registers the initial `__heap_base .. __heap_end` region with dlmalloc.

Our `vm_init` calls `malloc` without that initialization. The same libc allocator
source is linked, but its initializer is static and is not called by our bridge.
The generated `sbrk` uses the current memory end for new pages.

The probe observed:

```text
initial linear memory length       393,216 bytes
first malloc(16) returned pointer  393,232
linear memory after allocation     458,752 bytes

  initial pages                    newly grown page
  +------------------------------+-------------------+
  | stack/data | unused heap tail| malloc starts here |
  +------------------------------+-------------------+
                                393,216
```

This demonstrates the first allocation starts outside the initial memory span.
The amount of reclaimable initial tail depends on linked heap boundaries and
must be measured after initialization is wired correctly; the diagram does not
imply the entire initial span is unused.

### 4.4 Growth and snapshots amplify scratch sizing

`Memory.Grow` appends to a Go slice. Growth can allocate a new backing array and
copy existing linear memory; its capacity may exceed its logical length.
`Image` copies the full logical length, including idle arena storage and unused
pages. `Load` copies the snapshot back but does not shrink larger memory or clear
the tail beyond the image.

```text
oversized scratch or one large input
                |
                v
guest allocator needs more pages
                |
                +--> possible Go backing-array allocation/copy
                |
                +--> larger memory high-water mark
                |
                +--> larger later snapshots, if captured after growth

free spill / rewind arena ----> reusable guest allocation space
                               NOT smaller Go backing memory
```

This matters especially for `Program`, which restores a baseline image after
calls. Shrinking a baseline reduces restore-copy traffic; it does not
automatically shrink every instance's high-water residency.

## 5. What the alternatives actually simplify

### 5.1 SQLite: ownership follows the native API

The SQLite wrapper does not impose one ownership convention on every call.

```text
small temporary arguments/out slots:
    arena or stack mark -> call -> reset

retained input string/blob:
    Go -> guest malloc + copy -> SQLite takes ownership
                                  |
                                  +-> sqlite3_free destructor later

result text/blob:
    SQLite-owned storage -> pointer + length -> immediate Go copy
                                            \-> explicit borrowed Raw API

callback arguments:
    sqlite3_value* array -> temporary Go wrappers -> callback ends
```

`sqlite3_columns_go` batches scalar values and pointer/length descriptors. It
does not copy every text/blob payload into another result arena. Go's `Columns`
copies data into durable values; `ColumnsRaw` exposes explicitly borrowed views.

The lesson is not “use malloc everywhere.” It is “make ownership explicit at the
API boundary, use small scratch storage, and avoid intermediate copies when the
library already owns stable result memory.”

SQLite also has explicit destruction hooks for Go-side registrations. Its
simple handle table benefits from those hooks; Python object lifetime and Go
cleanup scheduling are a different problem. SQLite's string ownership transfer
cannot be copied directly into `mp_obj_new_str`, which creates its own Python
storage rather than accepting a destructor-managed external buffer.

Its `StackMark`/`StackAlloc` helpers are useful for small fixed scratch records.
They are not a license to put arbitrary payloads on our guest stack: stack
bounds, alignment, pointer spilling, GC scans, and non-local unwinding must all
remain correct.

The tiny SQLite `main.c` is mostly an amalgamation/composition unit. Combining
our C files would not remove the ownership machinery. Explicit allocator
initialization is its more consequential pattern for this project.

### 5.2 MicroPython WebAssembly: shallow proxies first

The upstream proxy encoder does not recursively serialize containers:

```text
Python result
    |
    +-- scalar ------> inline descriptor --------> JS scalar
    |
    +-- string ------> borrowed pointer/length --> immediate JS copy
    |
    +-- other object -> root-table slot ---------> JS proxy
                                                   |
                                              optional toJs()
                                                   |
                                      separate container traversal
```

It does not eagerly render every opaque object's `repr`. `PyProxy.toJs` is the
separate deep-copy operation. The example implementation obtains raw list/dict
storage and converts children individually.

Guest roots plus weak host caching allow reuse of a live proxy. The temporary
`proxy_js_existing` strong reference bridges the weak lookup and return handoff.
If an older proxy is dead but its finalizer has not run, a new proxy can receive
a different slot; freeing the old slot must not delete the new reverse mapping.
These are essential parts of the protocol, not optional cache details.

Do not copy the port indiscriminately:

- Its integer conversion has an explicit big-integer limitation; our `int64`
  and bigint behavior should not regress.
- Its deep conversion/raw layout access is not a ready-made mutation-safe,
  cycle-safe serialization contract.
- Some JS allocation cleanup paths are not structured as `try/finally`.
- Direct finalizer calls into the guest do not match our concurrent Go cleanup
  model. Keep deferred releases under the instance lock.
- Under `MICROPY_GC_SPLIT_HEAP_AUTO`, collection is deferred until the top-level
  boundary and the heap can grow during execution. The alternate branch scans
  stack/registers. Neither branch is a drop-in replacement for our bounded
  Python heap and wasm2go stack-root strategy.

### 5.3 Comparison

| Question | Current bridge | SQLite examples | WebAssembly proxies | Proposed direction |
|---|---|---|---|---|
| Ordinary guest output | Complete recursive wire tree | Scalars and borrowed descriptors | Scalars, borrowed strings, proxies | Shallow/batched descriptors, eager Go materialization |
| Payload storage | Extra output copy | Library-owned | Guest-owned for strings | Borrow while explicitly rooted; copy once into Go |
| Deep containers | Recursion in C and Go | Not applicable | Separate host conversion | Recursion policy in Go; C bulk accessors |
| Opaque descriptions | Eager Python `repr` | Not applicable | Not eager | Explicit description operation |
| Temporary storage | 64 KiB arena; 16 KiB result; 32 KiB callback stack | 4 KiB arena plus small stack scratch | Small records plus individual allocations | Small bounded scratch/batches; owned spills where necessary |
| Lifetime tracking | Acquisition counts and sidecar list | API-specific destructors/lifetimes | Roots, weak cache, finalizer | Scoped conversion roots plus persistent handles |

## 6. Proposal A: simplify allocation before changing semantics

This is the lowest-risk first step.

1. Expose and call libc allocator initialization exactly once for a fresh
   module, before its first allocation. Restore allocator state from snapshots;
   do not initialize dlmalloc again over a restored heap.
2. Measure a 32 KiB module arena with the current 16 KiB output protocol.
   Separate input scratch sizing from output sizing instead of defining one as
   four times the other.
3. Keep the existing spill and mark behavior. Bounds/alignment/lifecycle checks
   belong in a small arena implementation, not at every caller.
4. Measure startup allocated bytes, resident slice capacity, snapshot size, and
   pool restore time as well as call latency.

Expected benefit: less startup growth/copying and smaller baseline snapshots.
No claimed speedup until tested. Do not shrink the linked stack in the same
change; remove callback stack payloads first and measure stack high-water usage.

## 7. Proposal B: shallow guest output with bulk materialization

This is the main architectural proposal and requires a prototype.

### 7.1 Preserve eager public values

The public API continues to return Go-owned builtin values from `Eval`, `Call`,
and callback argument conversion. Internal temporary handles are released
deterministically once conversion completes. Only opaque objects and the
existing cycle/depth fallback need persistent Go handles.

```text
                   +----------------------------+
                   | Public Value remains eager |
                   +-------------+--------------+
                                 |
                      Go materializer / codec
                      recursion + cycle policy
                                 |
                    +------------+------------+
                    |                         |
             scalar/blob result        container/opaque result
                    |                         |
             inline scalar or         explicit rooted read scope
             rooted blob view                 |
                    |                         |
             copy borrowed bytes       bounded shallow batches
                    |                         |
                    +------------+------------+
                                 |
                       Go-owned data + handles
                                 |
                  release temporary roots / scratch
```

This reverses the earlier “avoid host-driven walking” goal in `DESIGN.md`.
The justification is not that walking is automatically faster. It is that
**bulk** shallow access can avoid a second full object tree, duplicated recursive
conversion, and fixed whole-result capacity. A per-element RPC-style design
would recreate the old overhead and is not the proposed fast path.

wasm2go calls are Go calls, not network requests or cgo transitions. They still
have generated-code and bridge overhead, so batch sizes must be measured.

### 7.2 Transport contract

Keep a fixed, little-endian descriptor with explicit C size assertions. Twelve
bytes remains a useful starting point, not a constraint that justifies hiding
ownership in more pointer bits. Separate transport status, borrowed object
tokens, persistent reference IDs, and optional description data conceptually.

The following are illustrative operations, not final function signatures:

```text
execute(...)                    -> scalar descriptor or rooted result
describe(rooted value)           -> kind and scalar/blob information
read_children(root, cursor, buf) -> initialized count + shallow records
retain(value in active scope)    -> persistent reference
end_read(scope)                  -> release temporary roots
describe_object(handle)          -> optional type/repr operation
```

Choose either bounded batches or whole shallow child blocks based on measured
workloads. For a wide primitive list, a batch contains inline numbers, not one
new persistent reference per number. Do not expose MicroPython's private dict
or set layouts as a public ABI; C accessors should hide them.

A completed batch can be decoded immediately. If the full Go value is too large,
enforce explicit byte/node/depth budgets rather than depending on a 16 KiB wire
buffer to limit it accidentally.

Inline scalars should not require a heap allocation or persistent handle. For
borrowed strings/bytes, a reusable root slot reserved before execution is one
candidate for avoiding a new handle allocation on every result. Nested read
scopes still require distinct slots. Benchmark this against copying short
payloads: saving a `memcpy` does not guarantee a win if root-management overhead
dominates. Keep small fixed output records in scratch storage throughout.

### 7.3 Borrowing is the hard contract

A pointer into guest linear memory does not prove that its Python allocation is
alive. The instance lock prevents another API operation from interfering, but
the current operation can still trigger user code.

```text
root Python result
       |
       +--> object graph stays reachable
       |
read descriptor ------> borrow bytes ------> Go copies bytes
       |                                      |
       |     no invalidating action here      |
       +--------------------------------------+
                                              |
                                      end borrow / release root
```

The prototype must define these rules before removing existing protections:

- A result root is established before the producing guest frame returns, or
  consumption occurs synchronously while an explicitly rooted frame is live.
  A raw returned object address in a Go field is insufficient.
- Every borrowed blob/child remains rooted until copied or promoted. A parent
  root alone is insufficient if user code can remove that child from the parent.
- Shallow accessors must identify whether they allocate, run Python, or can
  trigger GC/finalizers. “Does not call `repr`” is not the same as “cannot mutate.”
- Retaining an object can grow the guest root table and trigger GC. Protect
  unread children and any previously borrowed data before that allocation.
- Keep container membership snapshots where invalidating calls are possible.
  Remove them only for access phases proven non-allocating and non-mutating.
  Snapshot construction itself must not reuse a stale length or storage pointer
  after an allocation that can run finalizers.
- Do not retain a Go slice view of linear memory across a call that can grow
  it. Keep offsets and reacquire views afterward; copy escaping bytes.
- Release read scopes on conversion errors, Python exceptions, cancellation,
  and ordinary completion. Support nesting for callbacks; a single global
  “current result” root would be overwritten by nested work.

The existing snapshots address child `repr` mutation during recursive output.
They are not by themselves proof of safety against finalizers during snapshot
allocation. That scenario needs a dedicated adversarial test.

Prefer an explicit scoped root/snapshot protocol over disabling GC for an
unbounded conversion. Disabling GC can make a small configured Python heap fail
on values that otherwise fit after collection.

### 7.4 Reference ownership during the migration

Retain the current counts, owner checks, and deferred cleanup queue initially.
Changing output shape and reference identity together would obscure failures.

Distinguish temporary acquisitions from public Go handles:

```text
temporary container/root acquisition
    -> host materializes builtin value
    -> explicit release at scope exit

opaque acquisition
    -> successful promotion to Go *Ref
    -> existing asynchronous cleanup / queued release
```

Do not call `Track` for every temporary container and then wait for Go GC. That
would replace output-buffer pressure with guest-root pressure.

If a batch acquires references, it still needs rollback ownership. A flat batch
with an initialized-record count can support a simple bounded cleanup loop;
alternatively a read scope can own all temporary roots. The exact choice must
be settled by the prototype. Merely deleting the current sidecar list is unsafe.

The target removes recursive C result serialization, full-tree result arenas,
and descriptions embedded in every opaque value. It does not claim all
temporary-root, mutation, and cleanup machinery can disappear.

### 7.5 Remove eager descriptions deliberately

Transporting an object should not normally execute its arbitrary `__repr__`.
Separate returning an object from describing it.

```text
current:
    return object -> execute repr -> allocate text -> copy text -> pin object

proposed:
    return object -> establish root -> descriptor/handle
    explicit description request -> execute repr under normal error handling
```

This avoids repeated rendering and an important source of mutation/exception
paths. However, current Go objects cache their descriptions and can display
them without a live interpreter. Lazy descriptions are a behavior change.
Choose and document one of: explicit description API, a non-executing type-only
default description, or an opt-in compatibility mode. Do not hide a guest call
inside an apparently pure `String`/`Export` method.

### 7.6 Callbacks without two large stack arenas

Apply the same guest-to-host materializer to callback arguments while their
guest frame/read scope is alive. Preserve current behavior by producing eager,
retainable Go callback arguments before invoking application code.

For callback returns, reuse the existing Go input encoder in **host-owned**
scratch rather than a fixed C-stack payload region. Its scope must last until
C finishes decoding the return:

```text
C callback frame                         Go callback scope
       |                                       |
       +---------- begin callback ------------>|
       |                               decode args, invoke Go
       |                               encode return in host arena
       |<--------- return record pointer ------+
       |
       +-- obj_from_value(return pointer)
       |
       +---------- finish callback -----------> reset return arena mark
```

`finish` must run for both decode success and guest exception unwinding.
A Go `defer arena.Mark()()` inside the trampoline would run too early: C has
not read the returned pointer yet. An alternative is synchronous C consumption
before the trampoline returns, but that needs carefully contained non-local
unwinding and error reporting. These are protocol alternatives, not changes to
make simultaneously.

The goal is a small fixed C frame plus scoped host scratch, removing the two
16 KiB stack payload reservations and the borrowed-return-arena overflow mode.
Bound total callback return sizes explicitly even when the arena can spill.

## 8. Proposal C: shared persistent handles, evaluated separately

A weak cache could make repeated exports of the same live Python object share
one Go `*Ref`. This may remove repeated cleanup registrations and eventually
allow one guest pin per live proxy instead of acquisition counts.

```text
guest object address
        |
        v
host weak cache -------- live handle --------> reuse same *Ref
        |
        +-- dead/missing --------------------> establish new handle + pin
                                                  |
                                             cleanup queues token
                                                  |
                                             safe-point release
```

This is attractive for repeatedly returning the same function or object, but
it is not a trivial replacement of `Track`:

- Hold a strong temporary reference between weak lookup and handoff.
- Do not let an old queued cleanup unpin a newly created replacement.
- Do not reuse the old slot while its cleanup is still responsible for it,
  unless an explicit generation/token protocol makes that safe.
- Remove reverse mappings only if they still name the released handle.
- Preserve interpreter/timeline owner identity across restore.
- Retain deterministic temporary-scope cleanup; weak caching does not solve
  partial decode ownership or timely release of fresh short-lived objects.

Recommendation: defer this change. If the simpler output protocol already
meets performance and memory goals, a count-based reference owner may be the
more understandable long-term choice.

## 9. Alternatives not recommended as the first change

### Fully lazy public proxies

This most closely follows the WebAssembly port and can avoid all unrequested
deep conversion. It also changes `AsList`, `Export`, object descriptions, and
use after interpreter close/restore. `Program` restores its pooled instance
before the caller uses a returned value, making live proxies especially awkward.
Offer a separate scoped/proxy API only if those semantics are wanted.

### Growable recursive output arena

This preserves one-call deep conversion and can remove the 16 KiB ceiling.
However, it retains recursive C serialization, duplicate payload copies,
mutation protection, and transfer acquisition cleanup. Reallocation must not
invalidate absolute internal pointers; use offsets or stable chunks. It is a
capacity improvement, not the strongest simplification.

### Synchronous streaming into a Go result builder

C could walk the result and import events such as scalar/list-start/list-end.
Go would build the value directly, removing the full output tree. This retains
C recursion/mutation concerns, adds per-event host calls, and requires nested
builder/error state. Benchmark only if batched pull access is unsatisfactory;
it is not clearly simpler overall.

### Copying SQLite's borrowed callback API verbatim

SQLite callback `Value` wrappers borrow guest values. Our callback values can
currently be retained. Replacing them with borrowed wrappers without a new API
contract would introduce use-after-scope behavior.

### Removing stack roots or collecting only between calls

Our guest can allocate heavily in one long-running operation and uses a small
configured heap. It needs correct in-call GC. Retain pointer spilling, stack
scanning, and conservative-root handling until a separately proven GC design
replaces them.

## 10. Performance baseline and acceptance criteria

Existing benchmarks, Apple M3 Pro / darwin-arm64, one preliminary 200 ms run per
case on the current generated guest:

| Benchmark | Time/op | Go bytes/op | Go allocations/op |
|---|---:|---:|---:|
| No-argument call | 574 ns | 152 | 4 |
| One integer echo | 794 ns | 152 | 4 |
| 1 KiB string echo | 3.92 us | 1,192 | 6 |
| 1 KiB bytes echo | 3.44 us | 1,200 | 6 |
| 100-integer list echo | 21.4 us | 1,968 | 6 |
| Nested echo | 7.16 us | 900 | 32 |
| Stateful instance add | 1.04 us | 152 | 4 |
| Pooled program add | 17.2 us | 320 | 7 |
| Instance creation/close | 145 us | 1,831,017 | 572 |

These are end-to-end measurements, not isolated serialization costs. Go
allocated bytes do not include guest allocations served inside existing linear
memory and do not equal retained memory. The pooled/stateful difference includes
restore and pool handling; it cannot all be attributed to copying the arena.

Reproduce this preliminary run from the repository root:

```sh
go test . -run '^$' \
  -bench '^(BenchmarkCall|BenchmarkProgramVsInstance|BenchmarkInstanceAllocation)$' \
  -benchmem -benchtime=200ms -count=1
```

The sizing probe is intentionally simple to reproduce in an internal host test:
construct `newModule(nil)`, measure `len`/`cap` of `*mem.Slice()`, initialize the
128 KiB VM, allocate each candidate arena, and measure again. Test each arena
with a fresh module. A separate fresh module records its first `Alloc(16)`.

Before adopting Proposal B, compare a rebuilt baseline and prototype using
repeated longer benchmark runs. Include:

- Scalars and tiny containers: no material regression in the common fast path.
- Wide flat containers and nested containers: compare per-element, full shallow
  block, and bounded batches; measure guest crossings as well as time.
- Large strings/bytes: demonstrate removal of the intermediate output copy and
  successful results beyond 16 KiB without reexecuting Python.
- Repeated same-object exports versus fresh-object churn: measure handle
  allocations, pending releases, root-table high-water size, and guest free heap.
- Callback scalars, large callback payloads, and nested callbacks originating
  from conversion/finalizers: measure guest stack high-water usage.
- Small Python heaps: distinguish true live-object cost from delayed Go cleanup
  and temporary conversion roots.
- Startup, snapshot, clone, and pooled calls: measure logical pages, backing
  capacity, total Go allocations, and image copy size separately.

Correctness gates must include cycles/depth limits, bigint fidelity, aliasing,
retained callback arguments, owner mismatch, restore with pending cleanups,
partial batch failure, allocation failure during retain, `repr` mutation, GC
finalizer mutation during snapshot creation, cancellation, and exact-once scope
cleanup. Existing transfer/arena/reference tests remain useful regression
coverage, but they are not sufficient proof for a new borrowing protocol.

## 11. Migration and decision gates

```text
baseline + memory measurements
             |
             v
allocator initialization + scratch-size experiment
             |
             v
shallow output prototype behind internal boundary
             |
             +-- scalar/blob fast path
             +-- rooted, batched container access
             +-- eager Go materialization
             +-- existing persistent reference owner
             |
             v
correctness + performance gates
             |
             +-- fail --> retain current ABI; keep independent allocation wins
             |
             +-- pass --> migrate callbacks and exception/description paths
                              |
                              v
                    remove superseded recursive output machinery
                              |
                              v
                    evaluate weak handle caching separately
```

Fix known correctness issues independently rather than depending on a redesign:
packed reference IDs currently conflict with a signed negative-error return;
counts lack overflow checks; transfer reads/list cleanup are not region-bounded;
and arena lifecycle assumptions are largely caller-enforced. A new protocol
must not silently inherit these weaknesses.

The intended final model is:

```text
temporary Go inputs     -> host arena scope
temporary guest outputs -> rooted read scope + immediate Go copy
persistent Python state -> guest pin + owned Go handle
```

That introduces an explicit borrowed-read lifetime, but can delete considerably
more recursive transfer machinery than it adds. Adopt it only if the prototype
demonstrates that the root/invalidation rules remain smaller and easier to test
than the current arena-tree protocol.
