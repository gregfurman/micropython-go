// Package micropython embeds MicroPython in Go without CGO.
//
// Use [NewInstance] for an interpreter that keeps state between calls, or
// [NewProgram] to start each run from the same initialized Python state.
// Close instances and programs when done.
//
// Calls accept Go values and return [Value] results. Use [Value.Export] for
// ordinary Go data or the As methods for checked, type-specific access.
// Filesystem access, networking, and output are configured through options.
package micropython
