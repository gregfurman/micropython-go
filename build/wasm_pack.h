// Wire format for arguments coming from the host. The encoder is
// internal/exec/pack.go; internal/exec/pack_abi_test.go checks it against this
// file, so this is the definition both sides answer to.

#ifndef MICROPY_INCLUDED_WASI_WASM_PACK_H
#define MICROPY_INCLUDED_WASI_WASM_PACK_H

// One byte of tag per value, in a flat prefix walk. Containers carry a u32
// count and are followed by that many values, twice that for a dict.
// Integers, floats and lengths are little-endian; integers and floats are 8
// bytes, lengths 4.
enum {
    PK_NONE = 0,
    PK_FALSE = 1,
    PK_TRUE = 2,
    PK_INT = 3,
    PK_FLOAT = 4,
    PK_STR = 5,
    PK_BYTES = 6,
    PK_LIST = 7,
    PK_TUPLE = 8,
    PK_DICT = 9,
    PK_SET = 10,
    PK_FROZENSET = 11,
};

#define PK_MAX_DEPTH (32)

#endif // MICROPY_INCLUDED_WASI_WASM_PACK_H
