// Package env implements the guest's os environment imports.
package env

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/gregfurman/micropython-go/internal/host/abi"
	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/micropython"
)

var _ micropython.Xos = (*Environment)(nil)

const (
	// Host configuration may exceed maxVariables. Guest writes can only add a
	// name while the total number of variables is below this limit.
	maxVariables   = 256
	maxNameLength  = 1024
	maxValueLength = 65536
)

func validName(name string) bool {
	return name != "" && len(name) <= maxNameLength && !strings.ContainsAny(name, "=\x00")
}

func validValue(value string) bool { return !strings.ContainsRune(value, 0) }

// Validate reports whether a pair can be configured. The guest is held to the
// same rules, so a pair rejected here is one it could never read back.
func Validate(name, value string) error {
	switch {
	case name == "":
		return errors.New("name is empty")
	case len(name) > maxNameLength:
		return fmt.Errorf("name is %d bytes, over the %d byte maximum", len(name), maxNameLength)
	case strings.ContainsAny(name, "=\x00"):
		return fmt.Errorf("name %q contains %q or NUL", name, "=")
	case len(value) > maxValueLength:
		return fmt.Errorf("value of %s is %d bytes, over the %d byte maximum", name, len(value), maxValueLength)
	case !validValue(value):
		return fmt.Errorf("value of %s contains NUL", name)
	}

	return nil
}

// Environment holds one guest's variables. It never reads or writes the Go
// process environment: the guest sees what the host handed it plus its own
// putenv calls, and those reach nothing outside this instance. Serialize its
// methods with guest execution.
type Environment struct {
	mem    *memory.Memory
	vars   map[string]string
	closed bool
}

// New copies vars, which may be nil for an empty environment.
func New(mem *memory.Memory, vars map[string]string) *Environment {
	return &Environment{mem: mem, vars: maps.Clone(vars)}
}

// Close discards the variables. Guest writes were never visible elsewhere.
func (e *Environment) Close() {
	e.closed = true
	e.vars = nil
}

// Reset restores snapshot variables before a rewind, discarding later writes.
func (e *Environment) Reset(vars map[string]string) {
	e.vars, e.closed = maps.Clone(vars), false
}

// Vars copies the current variables for a snapshot.
func (e *Environment) Vars() map[string]string { return maps.Clone(e.vars) }

func (e *Environment) view(ptr, size int32) ([]byte, int32) {
	if e.mem == nil {
		return nil, -abi.EFAULT
	}

	b, err := e.mem.View(ptr, size)
	if err != nil {
		return nil, -abi.EFAULT
	}

	return b, 0
}

func (e *Environment) name(ptr, size int32) (string, int32) {
	if e.closed {
		return "", -abi.EBADF
	}

	b, status := e.view(ptr, size)
	if status != 0 {
		return "", status
	}

	name := string(b)
	if !validName(name) {
		return "", -abi.EINVAL
	}

	return name, 0
}

// Xhost_env_get returns the value's full length. A value longer than valueCap
// is not written, leaving the guest to retry with a buffer that fits, so the
// output pointer is only checked once there is something to put in it.
func (e *Environment) Xhost_env_get(ptr, size, valuePtr, valueCap int32) int32 {
	name, status := e.name(ptr, size)
	if status != 0 {
		return status
	}

	if valueCap < 0 {
		return -abi.EINVAL
	}

	value, ok := e.vars[name]
	if !ok {
		return -abi.ENOENT
	}

	if len(value) > int(valueCap) {
		return int32(len(value))
	}

	out, status := e.view(valuePtr, int32(len(value)))
	if status != 0 {
		return status
	}

	copy(out, value)

	return int32(len(value))
}

func (e *Environment) Xhost_env_set(ptr, size, valuePtr, valueSize int32) int32 {
	name, status := e.name(ptr, size)
	if status != 0 {
		return status
	}

	b, status := e.view(valuePtr, valueSize)
	if status != 0 {
		return status
	}

	if len(b) > maxValueLength {
		return -abi.ENOMEM
	}

	value := string(b) // Own a copy; guest memory is borrowed for this call.
	if !validValue(value) {
		return -abi.EINVAL
	}

	if _, replacing := e.vars[name]; !replacing {
		if len(e.vars) >= maxVariables {
			return -abi.ENOMEM
		}

		if e.vars == nil {
			e.vars = make(map[string]string)
		}
	}

	e.vars[name] = value

	return 0
}

// Unsetting a name that was never set is not an error, as in CPython.
func (e *Environment) Xhost_env_unset(ptr, size int32) int32 {
	name, status := e.name(ptr, size)
	if status != 0 {
		return status
	}

	delete(e.vars, name)

	return 0
}
