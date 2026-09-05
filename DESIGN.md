# Arena-Based Value Transfer Design

## 1. Overview

`micropython-go` embeds MicroPython compiled to WebAssembly and hosts it from Go.

Go and MicroPython exchange values through WebAssembly linear memory using a small, stable ABI. The new design replaces per-value allocation and host-driven container walking with **arena-backed value trees**.

The core rule is:

> Values are encoded as fixed-size `mp_value_t` records. Variable-sized payloads and nested values live in a caller-owned transfer arena.

This applies in both directions:

* MicroPython → Go
* Go → MicroPython

The codec understands the ABI, but does not own transfer memory.

---

## 2. Goals

The design should:

* support common Python builtins without cgo
* keep the Wasm ABI small and stable
* avoid per-value `malloc` / `free`
* avoid host-driven walking of lists, tuples, dicts, and sets
* make ownership and lifetime explicit
* support nested values recursively
* retain references for opaque Python objects
* make guest → host and host → guest transfer symmetrical
* allow the host to control transfer buffer sizes
* make it possible to retry with larger buffers later

---

## 3. Non-goals

The first version does not need to:

* preserve identity of copied builtin containers
* preserve arbitrary cyclic builtin containers
* dynamically grow a C-side transfer arena
* represent arbitrary Python object internals
* eliminate references for opaque objects
* support values larger than the configured transfer capacity without error or retry

Cyclic builtin structures should be detected and rejected rather than represented as copied trees.

Example:

```python
x = []
x.append(x)
```

cannot be represented as a finite copied value tree.

---

## 4. Value ABI

All values use a fixed 12-byte record.

### C

```c
typedef struct {
    uint32_t kind;
    uint32_t w1;
    uint32_t w2;
} mp_value_t;
```

### Go

```go
const ValueSize = 12

type Value struct {
    Kind Kind
    W1   uint32
    W2   uint32
}
```

The ABI properties are:

```text
size      = 12 bytes
alignment = 4 bytes
stride    = 12 bytes
```

The struct should be statically checked on the C side.

```c
_Static_assert(sizeof(mp_value_t) == 12, "mp_value_t size");
_Static_assert(_Alignof(mp_value_t) == 4, "mp_value_t alignment");
```

Every value occupies 12 bytes regardless of kind.

---

## 5. Value Representations

The meaning of `w1` and `w2` depends on `kind`.

### Scalars

```text
KIND_NONE
    w1 = 0
    w2 = 0

KIND_BOOL
    w1 = 0 or 1
    w2 = 0

KIND_INT
    w1 = low 32 bits of signed int64 representation
    w2 = high 32 bits

KIND_FLOAT
    w1 = low 32 bits of IEEE754 float64
    w2 = high 32 bits
```

### Variable-sized scalar values

```text
KIND_BIGINT
    w1 = byte length
    w2 = pointer to decimal representation

KIND_STR
    w1 = byte length
    w2 = pointer to UTF-8 bytes

KIND_BYTES
    w1 = byte length
    w2 = pointer to bytes

KIND_EXCEPTION
    w1 = encoded exception text length
    w2 = pointer to exception text
```

The pointed-to bytes live in the same transfer arena as the root value.

For zero-length payloads:

```text
w1 = 0
w2 = 0
```

is preferred.

---

## 6. Container Representation

Builtin containers are copied recursively.

### Lists, tuples, sets, frozensets

```text
kind = KIND_LIST / KIND_TUPLE / KIND_SET / KIND_FROZENSET
w1   = number of elements
w2   = pointer to mp_value_t[w1]
```

Example:

```python
[1, "abc"]
```

may be laid out as:

```text
root Value
    kind = LIST
    w1   = 2
    w2 ───────────────┐
                      │
                      ▼
               Value(INT)
               Value(STR)
                    │
                    └──────► "abc"
```

### Dicts

Dictionaries use alternating key and value records.

```text
kind = KIND_DICT
w1   = number of key/value pairs
w2   = pointer to mp_value_t[w1 * 2]
```

Layout:

```text
key0
value0
key1
value1
...
```

This avoids introducing a second ABI structure for dictionary entries.

---

## 7. Transfer Arenas

A transfer arena is a contiguous region of Wasm linear memory used for one encoded value tree.

The allocator is a simple 4-byte-aligned bump allocator.

Conceptually:

```c
typedef struct {
    uint8_t *base;
    uint32_t capacity;
    uint32_t offset;
} mp_arena_t;
```

Allocation:

```c
offset = align4(offset)

if size does not fit:
    fail

ptr = base + offset
offset += size
```

The arena owns no individual values.

The entire region is released or reset as one unit.

---

## 8. Ownership Model

Ownership is deliberately separated from encoding.

### Codec

The codec:

* understands the ABI
* reads and writes `Value` records
* resolves references
* does not own transfer memory
* does not free individual payload pointers

The desired invariant is:

> Codec never owns memory and never frees transfer memory.

### Arena

The arena:

* owns or represents the transfer region
* controls allocation lifetime
* provides space for nested payloads
* is reset or freed as a whole

### References

The reference table owns identity for opaque Python objects.

References are separate from copied transfer memory.

---

## 9. Two Arena Roles

There are two related but distinct arena concepts.

### Go arena

The Go host may own reusable Wasm memory used for guest operation results.

It manages:

* base allocation
* offsets
* marks/resets
* optionally overflow allocations

Example:

```go
reset := arena.Mark()
defer reset()
```

### C arena view

C does not need to own the result memory.

Instead, Go passes:

```text
outPtr
outCapacity
```

and C creates a temporary `mp_arena_t` view over that region.

Example:

```c
mp_arena_t arena = {
    .base = (uint8_t *)(uintptr_t)out_ptr,
    .capacity = out_capacity,
    .offset = sizeof(mp_value_t),
};
```

The first 12 bytes are reserved for the root `mp_value_t`.

The C arena exists only for the duration of the call.

---

## 10. MicroPython → Go

For operations such as:

* `eval`
* `call`
* `call_ref`
* `iterator_next`
* `get_global`

Go provides a result region.

Example ABI:

```c
int32_t eval(
    const char *code,
    uint32_t code_len,
    uint32_t out_ptr,
    uint32_t out_capacity);
```

The C side:

1. reserves the first 12 bytes for the root value
2. evaluates the Python operation
3. recursively serializes the resulting object into the arena
4. writes the root at `out_ptr`
5. returns bytes used or a negative status

Conceptually:

```text
Go allocates region
      │
      ▼
C creates arena view
      │
      ▼
MicroPython object
      │
      ▼
value_from_obj()
      │
      ▼
arena-backed Value tree
      │
      ▼
Go codec.Consume()
```

---

## 11. Go → MicroPython

Values passed from Go to MicroPython are already encoded in Wasm memory.

C's `obj_from_value()` recursively reads that tree.

It does not need an arena parameter.

Flow:

```text
Go value
   │
   ▼
Go encoder
   │
   ▼
Value tree in Wasm memory
   │
   ▼
obj_from_value()
   │
   ▼
MicroPython object
```

`obj_from_value()` should support:

* none
* bool
* int
* bigint
* float
* string
* bytes
* list
* tuple
* dict
* set
* frozenset
* refs / opaque objects
* exceptions where appropriate

---

## 12. Host Callbacks

Host callbacks reverse the normal direction.

A Python function calls into Go.

### Arguments

C serializes callback arguments into temporary C-owned scratch storage.

```text
argbuf
    top-level Value records

arg_arena
    nested values
    strings
    bytes
    containers
```

The host consumes these arguments synchronously.

Their lifetime only needs to cover `host_trampoline`.

### Return value

C also supplies a return arena to Go.

Host callback ABI:

```text
host_trampoline(
    funcID,
    argsPtr,
    numArgs,
    outPtr,
    outCapacity
)
```

Go writes the complete return value tree directly into this region.

The Go codec should therefore expose an operation similar to:

```go
func (c *Codec) EncodeInto(
    ptr int32,
    capacity int32,
    v any,
) error
```

The encoder internally keeps:

```go
type encoder struct {
    codec    *Codec
    base     uint32
    capacity uint32
    offset   uint32
}
```

The encoder is per-operation.

The `Codec` itself should not hold an arena.

---

## 13. Codec Design

The long-lived codec remains:

```go
type Codec struct {
    mem  *memory.Memory
    refs Refs
}
```

### Encoding

Encoding receives an output region explicitly.

```go
func (c *Codec) EncodeInto(
    ptr int32,
    capacity int32,
    v any,
) error
```

It creates a temporary encoder with:

```text
base
capacity
offset = ValueSize
```

Nested encoding recursively allocates from that region.

### Decoding

Decoding requires no arena argument.

```go
func (c *Codec) Consume(ptr int32) (value.Value, error)
```

It recursively follows arena pointers and produces Go-owned values.

The returned Go value must not retain slices pointing into temporary Wasm arena memory.

For example:

* strings become Go strings
* byte arrays are copied
* child containers become Go-owned collections

Once `Consume` returns, resetting the Wasm arena must be safe.

---

## 14. Recursive Decode

The old container-walking architecture is removed.

Previously:

```text
Consume root
    ↓
container ref
    ↓
seq_item / map_next
    ↓
host repeatedly asks C for elements
```

Now:

```text
Consume root
    ↓
read w2 pointer
    ↓
read Value[] directly
    ↓
decode recursively
```

A recursion limit should remain.

For example:

```go
const maxDecodeDepth = 128
```

This protects against pathological or malicious nesting.

It replaces the old `maxWalkDepth`; it is no longer related to reference walking.

---

## 15. References

References remain necessary for Python values whose identity or behaviour cannot reasonably be copied.

Examples:

* functions
* bound methods
* generators
* iterators
* class instances
* custom objects

For these:

```text
Python object
    ↓
ref_add()
    ↓
KIND_REF / KIND_OBJECT
    ↓
Go Ref
```

Copied builtin containers should no longer use references.

This produces a clean distinction:

```text
builtin transferable value
    → copied through arena

opaque Python object
    → reference table
```

---

## 16. Objects and Metadata

Any metadata associated with `KIND_OBJECT` should ideally also be moved into the transfer arena.

The old model may independently allocate object metadata and later free it from Go.

That mixed ownership should be removed.

Target rule:

```text
all serialized metadata → arena
object identity          → ref table
```

Once this is done, `Codec.Consume()` should contain no calls to `mem.Free()`.

---

## 17. Exceptions

Exceptions use the same transfer mechanism as all other variable-sized values.

`value_from_exception()` serializes exception text into the provided arena.

No exception payload is individually `malloc`ed.

On an error during a result-producing operation:

```c
arena.offset = sizeof(mp_value_t);
value_from_exception(&arena, exc, out);
```

This discards any partially serialized result and reuses the same output region for the exception.

The root value becomes:

```text
KIND_EXCEPTION
w1 = error data length
w2 = arena pointer
```

If even the fallback exception text cannot fit, a valid empty `KIND_EXCEPTION` may be written.

---

## 18. Arena Capacity

A reasonable initial default is:

```go
const defaultValueArenaCapacity = 16 * 1024
```

This is a default transfer size, not a semantic maximum.

The arena may contain:

* root values
* nested `Value` records
* strings
* bytes
* bigint text
* exception text
* dictionary entries
* alignment padding

The capacity does not need to be divisible by 12.

It should be 4-byte aligned.

A later implementation may retry failed operations with progressively larger output buffers.

---

## 19. Failure Handling

Arena exhaustion should be treated distinctly from Python exceptions.

Possible convention:

```text
>= 0    success, value is number of bytes used

< 0     ABI / transfer failure
```

For example:

```text
-1 invalid output region
-2 output arena too small
```

Python exceptions are encoded as `KIND_EXCEPTION` values rather than returned as negative transport statuses.

This keeps:

```text
Python-level failure
```

separate from:

```text
ABI / transport failure
```

---

## 20. Functions Removed by the New Design

Once copied containers are fully implemented, the following old mechanisms become unnecessary:

```text
value_from_container
seq_item
map_next
map_next_ext

value_release
values_release

host-side walk()
consumeAt() container walking
maxWalkDepth
walkScratch
```

Cleanup loops that repeatedly consume values solely to release guest allocations should also disappear.

---

## 21. Functions Retained

The following concepts remain:

```text
value_from_obj
obj_from_value
value_from_exception

ref_add
ref_get
release_ref
reset_refs

call_ref
iterator_next
```

`iterator_next` remains relevant for genuine iterator references, even though it is no longer needed to traverse builtin containers.

Result-producing versions should accept:

```text
outPtr
outCapacity
```

rather than a single 12-byte scratch pointer.

---

## 22. Module Changes

The old module-level:

```go
scratch int32
walkScratch []int32
```

can be removed once all users migrate.

A module may retain a reusable Go arena:

```go
arena Arena
```

for normal guest result buffers.

For example:

```go
func (i *Module) Eval(code string) (value.Value, error) {
    codePtr, free, err := i.mem.WriteString(code)
    if err != nil {
        return nil, err
    }
    defer free()

    reset := i.arena.Mark()
    defer reset()

    outPtr, err := i.arena.New(defaultValueArenaCapacity)
    if err != nil {
        return nil, err
    }

    used := i.mod.Xeval(
        codePtr,
        int32(len(code)),
        outPtr,
        defaultValueArenaCapacity,
    )

    if used < 0 {
        return nil, transferError(used)
    }

    return i.codec.Consume(outPtr)
}
```

The decoded Go value must be completely independent of the arena before `reset()` runs.

---

## 23. Core Invariants

The implementation should preserve the following invariants.

### ABI

```text
sizeof(Value) = 12
alignment     = 4
```

### Ownership

```text
Codec does not own transfer memory.
Codec does not free transfer payloads.
```

### Arena

```text
Each copied value tree belongs to exactly one transfer arena.
```

### Pointers

```text
Every non-zero payload pointer must point inside the active transfer region,
except explicit opaque-reference representations.
```

### Containers

```text
Copied builtin containers contain copied child Values, not refs.
```

### References

```text
Refs represent opaque identity, not ordinary builtin containers.
```

### Lifetime

```text
All data reachable from a root Value remains valid until decoding completes.
```

### Decoding

```text
Decoded Go values must not retain aliases into temporary Wasm memory.
```

---

## 24. Resulting Architecture

The final architecture is:

```text
                    ┌─────────────────────┐
                    │       Codec         │
                    │                     │
                    │ ABI encode/decode   │
                    │ refs                │
                    │ no ownership        │
                    └─────────┬───────────┘
                              │
              ┌───────────────┴───────────────┐
              │                               │
              ▼                               ▼
       Go → MicroPython                MicroPython → Go
              │                               │
      caller-owned arena              caller-owned arena
              │                               │
      Go encoder writes               C serializer writes
              │                               │
              ▼                               ▼
         Value tree                      Value tree
              │                               │
              ▼                               ▼
      obj_from_value()                  Codec.Consume()
              │                               │
              ▼                               ▼
      MicroPython objects                  Go values
```

References form a separate side channel for opaque Python identities:

```text
MicroPython object
      │
      ▼
   ref table
      │
      ▼
    Go Ref
```

This leaves the system with two simple lifetime models:

```text
copied data     → arena lifetime
opaque identity → reference lifetime
```

That distinction is the central design principle of the new ABI.
