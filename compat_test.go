package micropython

import (
	"embed"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gregfurman/micropython-wasi/internal/host"
)

//go:embed testdata/starlark
var starlarkFS embed.FS

const moduleFunction = `
class module:
    def __init__(self, name, **kwargs):
        self.__name__ = name
        for key, value in kwargs.items():
            setattr(self, key, value)

def load(*args):
    # no-op
    pass

# --- We must mock the Starlark built-ins that assert.star expects ---

def error(msg):
    raise Exception(msg)

def catch(f):
    try:
        f()
        return None
    except Exception as e:
        return str(e)

def matches(pattern, s):
    # Using simple substring matching if MicroPython's 're' module isn't available
    return pattern in s 

def _freeze(x):
    return x

def _floateq(x, y):
    # Simple float equality fallback
    return abs(x - y) < 1e-9
`

func TestStarlark(t *testing.T) {
	entries, err := starlarkFS.ReadDir("testdata/starlark")
	if err != nil {
		t.Fatalf("cannot load starlark tests: %s", err)
	}

	in, err := NewInstance(t.Context())
	if err != nil {
		t.Fatalf("could not create Instance: %s", err)
	}

	_, err = in.Exec(t.Context(), strings.ReplaceAll(moduleFunction, "assert.", "_assert."))
	if err != nil {
		var exc *host.Exception
		if errors.As(err, &exc) {
			t.Fatalf("could not load in module: %s", exc.Raw())

		}
		t.Fatalf("could not load in module module: %s", err)
	}

	assertModule, err := starlarkFS.ReadFile("testdata/starlark/assert.star")
	if err != nil {
		t.Fatalf("cannot load starlark tests: %s", err)
	}

	_, err = in.Exec(t.Context(), strings.ReplaceAll(string(assertModule), "assert.", "_assert."))
	if err != nil {
		var exc *host.Exception
		if errors.As(err, &exc) {
			t.Fatalf("could not load in assert module: %s", exc.Raw())

		}
		t.Fatalf("could not load in assert module: %s", err)
	}

	defer in.Close()

	for _, entry := range entries {
		if entry.Name() == "testdata/starlark/assert.star" {
			continue
		}

		in, err := in.Clone(t.Context())
		if err != nil {
			t.Fatalf("could not create Instance: %v", err)
		}
		defer in.Close()

		src, err := starlarkFS.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("could not load script at [%s]: %v", src, err)
		}

		resp, err := in.Exec(t.Context(), string(src))
		if err != nil {
			t.Fatalf("Instance.Exec failed: %v", err)
		}

		fmt.Printf("resp: %v\n", resp)

	}

}
