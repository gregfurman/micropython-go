package env

import (
	"strings"
	"testing"

	"github.com/gregfurman/micropython-go/internal/host/abi"
	"github.com/gregfurman/micropython-go/internal/host/memory"
)

// Four pages leaves room to stage a value past maxValueLength in guest memory.
func newTestEnv(t *testing.T, vars map[string]string) *Environment {
	t.Helper()
	e := New(memory.New(4, 8), vars)
	t.Cleanup(e.Close)
	return e
}

func put(t *testing.T, e *Environment, ptr int32, value string) int32 {
	t.Helper()
	if err := e.mem.Write(ptr, []byte(value)); err != nil {
		t.Fatal(err)
	}
	return int32(len(value))
}

func want(t *testing.T, got, expected int32) {
	t.Helper()
	if got != expected {
		t.Fatalf("got %d, want %d", got, expected)
	}
}

// read runs a get and returns the length plus whatever landed in the buffer.
func read(t *testing.T, e *Environment, name string, capacity int32) (int32, string) {
	t.Helper()
	size := put(t, e, 0, name)
	n := e.Xhost_env_get(0, size, 4096, capacity)
	if n <= 0 {
		return n, ""
	}
	written := n
	if written > capacity {
		return n, "" // Nothing was written; the caller is expected to retry.
	}
	b, err := e.mem.View(4096, written)
	if err != nil {
		t.Fatal(err)
	}
	return n, string(b)
}

func TestGetSetUnset(t *testing.T) {
	e := newTestEnv(t, map[string]string{"STAGE": "prod", "EMPTY": ""})

	if n, value := read(t, e, "STAGE", 64); n != 4 || value != "prod" {
		t.Errorf("STAGE = %d, %q", n, value)
	}
	if n, value := read(t, e, "EMPTY", 64); n != 0 || value != "" {
		t.Errorf("EMPTY = %d, %q", n, value)
	}
	if n, _ := read(t, e, "MISSING", 64); n != -abi.ENOENT {
		t.Errorf("MISSING = %d, want ENOENT", n)
	}

	name := put(t, e, 0, "ADDED")
	value := put(t, e, 512, "here")
	want(t, e.Xhost_env_set(0, name, 512, value), 0)
	if n, got := read(t, e, "ADDED", 64); n != 4 || got != "here" {
		t.Errorf("ADDED = %d, %q", n, got)
	}

	// Setting an existing name replaces rather than appends.
	replacement := put(t, e, 512, "x")
	want(t, e.Xhost_env_set(0, name, 512, replacement), 0)
	if n, got := read(t, e, "ADDED", 64); n != 1 || got != "x" {
		t.Errorf("after replace = %d, %q", n, got)
	}

	want(t, e.Xhost_env_unset(0, name), 0)
	if n, _ := read(t, e, "ADDED", 64); n != -abi.ENOENT {
		t.Errorf("after unset = %d, want ENOENT", n)
	}
	// Unsetting what was never there is not an error.
	want(t, e.Xhost_env_unset(0, name), 0)
}

// An oversized value reports its length and writes nothing, so the guest can
// size a buffer and ask again. The output pointer is not touched or checked.
func TestGetSizingProbe(t *testing.T) {
	e := newTestEnv(t, map[string]string{"BIG": strings.Repeat("v", 300)})

	name := put(t, e, 0, "BIG")
	want(t, e.Xhost_env_get(0, name, 4096, 128), 300)
	if b, err := e.mem.View(4096, 1); err != nil || b[0] != 0 {
		t.Fatalf("wrote into a buffer that could not hold the value: %v %v", b, err)
	}
	want(t, e.Xhost_env_get(0, name, 1<<20, 0), 300) // Unreachable pointer, unused.

	n, value := read(t, e, "BIG", 300)
	if n != 300 || value != strings.Repeat("v", 300) {
		t.Errorf("retry with an exact fit gave %d, %q", n, value)
	}
}

func TestNameAndValueValidation(t *testing.T) {
	e := newTestEnv(t, nil)

	for _, name := range []string{"", "A=B", "A\x00B", strings.Repeat("n", maxNameLength+1)} {
		size := put(t, e, 0, name)
		if got := e.Xhost_env_get(0, size, 4096, 64); got != -abi.EINVAL {
			t.Errorf("get %q = %d, want EINVAL", name, got)
		}
		if got := e.Xhost_env_unset(0, size); got != -abi.EINVAL {
			t.Errorf("unset %q = %d, want EINVAL", name, got)
		}
	}

	name := put(t, e, 0, "OK")
	value := put(t, e, 512, "a\x00b")
	want(t, e.Xhost_env_set(0, name, 512, value), -abi.EINVAL)
	want(t, e.Xhost_env_get(0, name, 4096, -1), -abi.EINVAL)

	// A value small enough to write still needs somewhere real to write it.
	set := put(t, e, 512, "v")
	want(t, e.Xhost_env_set(0, name, 512, set), 0)
	want(t, e.Xhost_env_get(0, name, 1<<20, 64), -abi.EFAULT)

	// Guest pointers outside linear memory are a fault, not a name.
	want(t, e.Xhost_env_get(1<<20, 4, 4096, 64), -abi.EFAULT)
	want(t, e.Xhost_env_set(1<<20, 4, 512, 1), -abi.EFAULT)
	want(t, e.Xhost_env_set(0, name, 1<<20, 4), -abi.EFAULT)
	want(t, e.Xhost_env_unset(1<<20, 4), -abi.EFAULT)
}

// The guest writes into a host map, so both its size and its growth are capped.
func TestLimitsBoundTheMap(t *testing.T) {
	e := newTestEnv(t, nil)

	oversized := put(t, e, 512, strings.Repeat("v", maxValueLength+1))
	name := put(t, e, 0, "BIG")
	want(t, e.Xhost_env_set(0, name, 512, oversized), -abi.ENOMEM)

	fits := put(t, e, 512, strings.Repeat("v", maxValueLength))
	want(t, e.Xhost_env_set(0, name, 512, fits), 0)

	value := put(t, e, 512, "v")
	for i := len(e.vars); i < maxVariables; i++ {
		size := put(t, e, 0, "name"+string(rune('a'+i%26))+string(rune('a'+i/26)))
		want(t, e.Xhost_env_set(0, size, 512, value), 0)
	}
	if len(e.vars) != maxVariables {
		t.Fatalf("filled to %d, want %d", len(e.vars), maxVariables)
	}

	overflow := put(t, e, 0, "ONE_TOO_MANY")
	want(t, e.Xhost_env_set(0, overflow, 512, value), -abi.ENOMEM)

	// Replacing an existing name does not grow the map, so it still works.
	existing := put(t, e, 0, "BIG")
	want(t, e.Xhost_env_set(0, existing, 512, value), 0)
}

// New and Vars copy, so neither the caller's map nor a snapshot aliases ours.
func TestCopiesAtEveryBoundary(t *testing.T) {
	configured := map[string]string{"STAGE": "prod"}
	e := newTestEnv(t, configured)

	configured["STAGE"] = "mutated"
	configured["LATE"] = "added"
	if n, value := read(t, e, "STAGE", 64); n != 4 || value != "prod" {
		t.Errorf("caller's map reached the guest: %d, %q", n, value)
	}
	if n, _ := read(t, e, "LATE", 64); n != -abi.ENOENT {
		t.Error("a key added after New reached the guest")
	}

	snapshot := e.Vars()
	snapshot["STAGE"] = "scribbled"
	if n, value := read(t, e, "STAGE", 64); n != 4 || value != "prod" {
		t.Errorf("Vars aliased the live map: %d, %q", n, value)
	}
}

// A nil environment is empty rather than denied: the guest can still putenv,
// and os.getenv reports the absence instead of raising.
func TestNilEnvironmentIsEmptyNotDenied(t *testing.T) {
	e := newTestEnv(t, nil)

	if n, _ := read(t, e, "ANY", 64); n != -abi.ENOENT {
		t.Errorf("nil environment = %d, want ENOENT", n)
	}
	name := put(t, e, 0, "OWN")
	value := put(t, e, 512, "v")
	want(t, e.Xhost_env_set(0, name, 512, value), 0)
	if n, got := read(t, e, "OWN", 64); n != 1 || got != "v" {
		t.Errorf("guest write into a nil map = %d, %q", n, got)
	}
}

func TestResetRestoresConfiguredVars(t *testing.T) {
	e := newTestEnv(t, map[string]string{"STAGE": "prod"})

	name := put(t, e, 0, "STAGE")
	value := put(t, e, 512, "scribbled")
	want(t, e.Xhost_env_set(0, name, 512, value), 0)
	added := put(t, e, 0, "GUEST")
	want(t, e.Xhost_env_set(0, added, 512, value), 0)

	e.Reset(map[string]string{"STAGE": "prod"})
	if n, got := read(t, e, "STAGE", 64); n != 4 || got != "prod" {
		t.Errorf("STAGE after reset = %d, %q", n, got)
	}
	if n, _ := read(t, e, "GUEST", 64); n != -abi.ENOENT {
		t.Error("a guest variable survived the reset")
	}
}

func TestClosedEnvironmentRefusesEverything(t *testing.T) {
	e := newTestEnv(t, map[string]string{"STAGE": "prod"})
	e.Close()

	name := put(t, e, 0, "STAGE")
	want(t, e.Xhost_env_get(0, name, 4096, 64), -abi.EBADF)
	want(t, e.Xhost_env_set(0, name, 512, 1), -abi.EBADF)
	want(t, e.Xhost_env_unset(0, name), -abi.EBADF)

	// Close is idempotent, and Reset brings the environment back.
	e.Close()
	e.Reset(map[string]string{"STAGE": "prod"})
	if n, got := read(t, e, "STAGE", 64); n != 4 || got != "prod" {
		t.Errorf("after reopen = %d, %q", n, got)
	}
}

func TestNoMemoryIsAFault(t *testing.T) {
	e := &Environment{vars: map[string]string{"STAGE": "prod"}}
	want(t, e.Xhost_env_get(0, 5, 4096, 64), -abi.EFAULT)
	want(t, e.Xhost_env_set(0, 5, 512, 1), -abi.EFAULT)
	want(t, e.Xhost_env_unset(0, 5), -abi.EFAULT)
}
