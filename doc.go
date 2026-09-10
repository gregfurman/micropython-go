// Package micropython embeds MicroPython in Go without CGO.
//
// Use [NewInstance] for an interpreter that keeps state between calls, or
// [NewProgram] to start each run from the same initialized Python state.
// Close instances and programs when done.
//
// Calls accept Go values and return [Value] results. Use [Value.Export] for
// ordinary Go data or the As methods for checked, type-specific access.
//
// Host access is opt-in. Use [WithFS] for files, [WithEnv] for environment
// variables, [WithStdout] for output, and [WithHostFunc] for Go callbacks.
// [WithTCPAccess] and [WithUDPAccess] grant outbound connections;
// [WithDNSResolver] separately enables DNS. These options work with both
// instances and programs. They do not impose a hard CPU or total-memory limit.
package micropython
