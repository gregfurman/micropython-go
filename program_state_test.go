package micropython

import (
	"context"
	"testing"
)

// Nothing a call does to the interpreter may reach the next one. The pool
// makes that structural: release rewinds to the snapshot before enqueueing, so
// "in the pool" means "at the snapshot".
func TestProgramCallsDoNotLeakState(t *testing.T) {
	p, err := Compile(context.Background(), `
counter = 0

def bump():
    global counter
    counter += 1
    return counter

def stash(v):
    global leaked
    leaked = v
    return v

def peek():
    return globals().get("leaked", "clean")
`)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// A global mutated on every call must read 1 every time, not 1, 2, 3.
	for i := range 5 {
		got, err := p.Call(t.Context(), "bump")
		if err != nil {
			t.Fatal(err)
		}
		if got != int64(1) {
			t.Fatalf("call %d: counter = %v, want 1 -- state survived the pool", i, got)
		}
	}

	// A global created by one call must not exist for the next.
	if _, err := p.Call(t.Context(), "stash", "dirty"); err != nil {
		t.Fatal(err)
	}
	got, err := p.Call(t.Context(), "peek")
	if err != nil {
		t.Fatal(err)
	}
	if got != "clean" {
		t.Errorf("peek = %v, want \"clean\" -- a global leaked through the pool", got)
	}
}
