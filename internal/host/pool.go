package host

import (
	"slices"
	"sync"
)

// A snapshot holds a clone of the module's entire linear memory: the Python
// heap plus roughly 1.2 MiB of module image, rounded up to whole 64 KiB pages.
// Sizes therefore start just over 1 MiB with the default heap and climb from
// there, so the classes span that range rather than the small-buffer range a
// general-purpose pool would use.
//
// Requests above the largest class are served by a plain allocation. Holding
// buffers that big between calls costs more memory than the allocation saves.
var moduleMemoryChunkClasses = []int{
	1 << 20,
	2 << 20,
	4 << 20,
	8 << 20,
	16 << 20,
	32 << 20,
}

// The pools hold *[]byte, not []byte: putting a slice into an interface boxes
// its header, which allocates on every Put and undoes much of the saving.
var moduleMemoryChunkPools = newMemoryChunkPools()

func newMemoryChunkPools() []sync.Pool {
	pools := make([]sync.Pool, len(moduleMemoryChunkClasses))
	for i := range pools {
		size := moduleMemoryChunkClasses[i]
		pools[i].New = func() any {
			b := make([]byte, size)
			return &b
		}
	}
	return pools
}

// getMemoryChunk returns a buffer of exactly size bytes, drawn from a pool when
// one of the size classes fits. The contents are whatever the previous user
// left behind, so the caller must overwrite the whole slice before reading it.
//
// The buffer should be handed back with putMemoryChunk, but it does not have to
// be: an unreturned buffer is simply collected as usual.
func getMemoryChunk(size int) []byte {
	if size <= 0 {
		return nil
	}
	i, _ := slices.BinarySearch(moduleMemoryChunkClasses, size)
	if i == len(moduleMemoryChunkClasses) {
		return make([]byte, size)
	}
	buf := moduleMemoryChunkPools[i].Get().(*[]byte)
	return (*buf)[:size]
}

// putMemoryChunk returns a buffer from getMemoryChunk to its pool. Because
// getMemoryChunk reslices down to the requested length, the class is recovered
// from the capacity. A buffer that did not come from a pool -- one larger than
// the biggest class, or one the caller resliced -- is ignored rather than
// rejected, since there is nothing a caller could usefully do about it.
func putMemoryChunk(b []byte) {
	i, ok := slices.BinarySearch(moduleMemoryChunkClasses, cap(b))
	if !ok {
		return
	}
	full := b[:cap(b)]
	moduleMemoryChunkPools[i].Put(&full)
}
