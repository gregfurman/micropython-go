package micropython

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/gregfurman/micropython-wasi/internal/value"
)

const handlerSrc = `
def score(row):
    total = row["a"] * 2 + row["b"]
    return {"id": row["id"], "score": total, "ok": total > 10}

def boom():
    raise ValueError("nope")
`

func newProgram(t *testing.T) *Program {
	t.Helper()
	p, err := Compile(context.Background(), handlerSrc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestProgramCall(t *testing.T) {
	p := newProgram(t)

	got, err := p.Call(t.Context(), "score", Of(map[string]any{"id": "r-1", "a": 4, "b": 5}))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": "r-1", "score": int64(13), "ok": true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("score = %#v, want %#v", got, want)
	}
}

// A raising handler comes back as an *Exception and leaves the Program usable.
func TestProgramError(t *testing.T) {
	p := newProgram(t)

	var exc *value.Exception
	if _, err := p.Call(t.Context(), "boom"); !errors.As(err, &exc) {
		t.Fatalf("got %v (%T), want *Exception", err, err)
	} else if exc.Type() != "ValueError" {
		t.Errorf("Type = %q, want ValueError", exc.Type())
	}

	if _, err := p.Call(t.Context(), "score", Of(map[string]any{"id": "x", "a": 1, "b": 1})); err != nil {
		t.Errorf("after error: %v", err)
	}
}

// The point of Program: parallel calls run rather than queue, because the pool
// grows from the snapshot instead of serialising onto one interpreter.
func TestProgramConcurrent(t *testing.T) {
	p := newProgram(t)

	const goroutines, each = 8, 25

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*each)

	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				row := map[string]any{"id": "r", "a": int64(g), "b": int64(i)}
				got, err := p.Call(t.Context(), "score", Of(row))
				if err != nil {
					errs <- err
					return
				}
				m, ok := got.(map[string]any)
				if !ok {
					errs <- errors.New("not a dict")
					return
				}
				if want := int64(g)*2 + int64(i); m["score"] != want {
					errs <- errors.New("wrong score")
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestProgramClose(t *testing.T) {
	p, err := Compile(context.Background(), handlerSrc)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Call(t.Context(), "score", Of(map[string]any{"id": "x", "a": 1, "b": 1})); !errors.Is(err, ErrClosed) {
		t.Errorf("after Close: %v, want ErrClosed", err)
	}
}

// Compile reports a failure in the source itself, rather than deferring it to
// the first call.
func TestCompileError(t *testing.T) {
	if _, err := Compile(context.Background(), "def broken(:\n"); err == nil {
		t.Fatal("expected a syntax error")
	}
}
