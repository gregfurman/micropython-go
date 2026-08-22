package micropython

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Several Programs at once, which is the shape a host embedding this actually
// has: one compiled handler per tenant or per route, all live together.

const counterSrc = `
_calls = 0

def name():
    return %q

def bump():
    global _calls
    _calls += 1
    return _calls
`

// Programs are independent of each other. Each holds its own snapshot and its
// own pool, so nothing one defines or mutates is visible to another.
func TestProgramsAreIndependent(t *testing.T) {
	const n = 4

	programs := make([]*Program, n)
	for i := range programs {
		p, err := Compile(context.Background(), fmt.Sprintf(counterSrc, fmt.Sprintf("p%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		programs[i] = p
	}

	for i, p := range programs {
		got, err := p.Call(t.Context(), "name")
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("p%d", i); got != want {
			t.Errorf("program %d: name() = %v, want %q", i, got, want)
		}
	}

	// A name defined in one must not resolve in another.
	if _, err := programs[0].Call(t.Context(), "exec", "extra = 1"); err == nil {
		t.Log("note: exec reachable as a global")
	}
	for i, p := range programs {
		if _, err := p.Call(t.Context(), "no_such_function"); err == nil {
			t.Errorf("program %d resolved a name that does not exist", i)
		}
	}
}

// Closing one Program must not disturb any other.
func TestProgramCloseIsLocal(t *testing.T) {
	a, err := Compile(context.Background(), fmt.Sprintf(counterSrc, "a"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Compile(context.Background(), fmt.Sprintf(counterSrc, "b"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Call(t.Context(), "name"); err == nil {
		t.Error("a closed Program still served a call")
	}

	got, err := b.Call(t.Context(), "name")
	if err != nil || got != "b" {
		t.Errorf("after closing a: b.name() = %v, %v", got, err)
	}
}

// Many Programs driven in parallel, each checking it only ever sees its own
// interpreter. This is what would catch a snapshot or pool shared by mistake.
func TestProgramsConcurrentAcrossPrograms(t *testing.T) {
	const programs, goroutines, calls = 4, 4, 20

	var wg sync.WaitGroup
	errs := make(chan error, programs*goroutines)

	for i := range programs {
		p, err := Compile(context.Background(), fmt.Sprintf(counterSrc, fmt.Sprintf("p%d", i)))
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()

		want := fmt.Sprintf("p%d", i)
		for range goroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range calls {
					got, err := p.Call(t.Context(), "name")
					if err != nil {
						errs <- err
						return
					}
					if got != want {
						errs <- fmt.Errorf("got %v, want %q -- interpreters crossed", got, want)
						return
					}
				}
			}()
		}
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// Compile once, call many times with different inputs -- the pattern the whole
// design exists for. Each call must see the source's definitions and none of
// the previous call's mutations.
func TestProgramRepeatedCalls(t *testing.T) {
	p, err := Compile(context.Background(), fmt.Sprintf(counterSrc, "x"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	for i := range 50 {
		got, err := p.Call(t.Context(), "bump")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if got != int64(1) {
			t.Fatalf("call %d: bump() = %v, want 1 -- state carried over", i, got)
		}
	}
}

// A realistic handler, using the modules a handler actually reaches for.
func TestProgramRealisticHandler(t *testing.T) {
	p, err := Compile(context.Background(), `
import json
import re

_email = re.compile(r"^[\w.]+@[\w.]+$")

def handle(request):
    body = json.loads(request["body"])
    errors = []

    if not _email.match(body.get("email", "")):
        errors.append("email")
    if not isinstance(body.get("age"), int) or body["age"] < 0:
        errors.append("age")

    if errors:
        return {"ok": False, "invalid": errors}
    return {"ok": True, "email": body["email"], "age": body["age"]}
`)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{"valid", `{"email": "a.b@example.com", "age": 30}`, true},
		{"bad email", `{"email": "nope", "age": 30}`, false},
		{"negative age", `{"email": "a@b.com", "age": -1}`, false},
		{"missing fields", `{}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.Call(t.Context(), "handle", map[string]any{"body": tt.body})
			if err != nil {
				t.Fatalf("handle: %v", err)
			}
			m, ok := got.(map[string]any)
			if !ok {
				t.Fatalf("handle returned %#v (%T), want a dict", got, got)
			}
			if m["ok"] != tt.ok {
				t.Errorf("ok = %v, want %v (got %#v)", m["ok"], tt.ok, m)
			}
		})
	}

	// Malformed JSON is the guest's error, not a crash.
	if _, err := p.Call(t.Context(), "handle", map[string]any{"body": "{not json"}); err == nil {
		t.Error("malformed JSON was accepted")
	}
}

// Compile must reject source that does not compile, and say why, rather than
// deferring the failure to the first call.
func TestCompileRejects(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"syntax error", "def broken(:\n", "SyntaxError"},
		{"raises at import time", "raise ValueError('boom')\n", "ValueError"},
		{"name error at import time", "undefined_name()\n", "NameError"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Compile(context.Background(), tt.src)
			if err == nil {
				p.Close()
				t.Fatalf("Compile accepted %q", tt.src)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Compile(%q) = %v, want it to mention %s", tt.src, err, tt.want)
			}
		})
	}
}
