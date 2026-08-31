package micropython

// The tests in this file pin the claims made in the README's Limitations
// section. A failure here means the README needs updating alongside whatever
// behaviour changed.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func raises(t *testing.T, in *Instance, src string) *PythonError {
	t.Helper()
	err := in.Exec(context.Background(), src)
	var exc *PythonError
	if !errors.As(err, &exc) {
		t.Fatalf("%s\n\tgave %v (%T), want a *PythonError", src, err, err)
	}
	return exc
}

func TestLimitOpenRaisesOSError(t *testing.T) {
	in := newT(t)

	for _, src := range []string{
		`open("/etc/hosts")`,
		`open("/etc/hosts", "r")`,
		`open("out.txt", "w")`,
	} {
		if got := raises(t, in, src).Type(); got != "OSError" {
			t.Errorf("%s raised %s, want OSError", src, got)
		}
	}

	// And sanity check that interpreter is unharmed by the refusal
	if got, err := in.Eval(t.Context(), "1 + 1"); err != nil || got != int64(2) {
		// this would be embarassing lol
		t.Errorf("after OSError: %#v, %v", got, err)
	}
}

func TestLimitImportCannotReachRealFiles(t *testing.T) {
	// Place a module in the cwd and expect an ImportError since no FS.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sidecar.py"), []byte("VALUE = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	in := newT(t)
	if got := raises(t, in, "import sidecar").Type(); got != "ImportError" {
		t.Errorf("import sidecar raised %s, want ImportError", got)
	}

	for _, mod := range []string{"json", "re", "sys"} {
		if err := in.Exec(t.Context(), "import "+mod); err != nil {
			t.Errorf("import %s: %v", mod, err)
		}
	}
}

func TestLimitNoOSModule(t *testing.T) {
	in := newT(t)

	if got := raises(t, in, "import os").Type(); got != "ImportError" {
		t.Errorf("import os raised %s, want ImportError", got)
	}

	// sys itself exists; it is the stream objects on it that do not.
	if err := in.Exec(t.Context(), "import sys"); err != nil {
		t.Fatalf("import sys: %v", err)
	}
	if got, err := in.Eval(t.Context(), "sys.platform"); err != nil || got != "wasi" {
		t.Errorf("sys.platform = %#v, %v; want \"wasi\"", got, err)
	}
	for _, stream := range []string{"sys.stdout", "sys.stderr", "sys.stdin"} {
		if got := raises(t, in, stream).Type(); got != "AttributeError" {
			t.Errorf("%s raised %s, want AttributeError", stream, got)
		}
	}
}

func TestLimitPrintGoesToWithStdoutNotHostStdout(t *testing.T) {
	// print() reaches the writer given to WithStdout, and (most importantly...)
	// nothing reaches the host's stdout.
	var out bytes.Buffer
	in, err := NewInstance(t.Context(), WithStdout(&out))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close() })

	onStdout := captureStdout(t, func() {
		if err := in.Exec(t.Context(), `
print("first")
print("second", 2)
`); err != nil {
			t.Error(err)
		}
	})

	if want := "first\nsecond 2\n"; out.String() != want {
		t.Errorf("WithStdout received %q, want %q", out.String(), want)
	}
	if onStdout != "" {
		t.Errorf("the process's stdout received %q, want nothing", onStdout)
	}

	// The sink is shared across calls, so a caller wanting one Exec's output on
	// its own resets between them.
	out.Reset()
	if err := in.Exec(t.Context(), `print("third")`); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "third\n"; got != want {
		t.Errorf("second Exec wrote %q, want %q", got, want)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w

	read := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		read <- string(b)
	}()

	fn()

	os.Stdout = orig
	w.Close()
	return <-read
}

const countFramesUntilOverflow = `
depth = 0
%s
raised = ""
try:
    %s
except BaseException as e:
    raised = type(e).__name__
`

func TestLimitStackDepth(t *testing.T) {
	// The README quotes this band. Frame cost varies with arguments and
	// locals, so the two ends are a trivial frame and a fat one.
	const minFrames, maxFrames = 340, 385

	tests := []struct{ name, def, call string }{
		{
			name: "no arguments",
			def:  "def rec():\n    global depth\n    depth += 1\n    rec()\n",
			call: "rec()",
		},
		{
			name: "four arguments and locals",
			def:  "def rec(a, b, c, e):\n    global depth\n    x = a + b\n    y = c + e\n    depth += 1\n    rec(x, y, a, b)\n",
			call: "rec(1, 2, 3, 4)",
		},
		{
			name: "bound method",
			def:  "class K:\n    def rec(self, a):\n        global depth\n        depth += 1\n        self.rec(a)\n",
			call: "K().rec(1)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := newT(t)
			if err := in.Exec(t.Context(), fmt.Sprintf(countFramesUntilOverflow, tc.def, tc.call)); err != nil {
				t.Fatal(err)
			}

			// It must be a catchable Python error, not a WASM trap.
			if got, err := in.Eval(t.Context(), "raised"); err != nil || got != "RuntimeError" {
				t.Errorf("overflow raised %#v, %v; want RuntimeError", got, err)
			}

			got, err := in.Eval(t.Context(), "depth")
			if err != nil {
				t.Fatal(err)
			}
			depth, ok := got.(int64)
			if !ok {
				t.Fatalf("depth = %#v (%T), want int64", got, got)
			}
			if depth < minFrames || depth > maxFrames {
				t.Errorf("recursed %d frames, outside the %d-%d the README documents",
					depth, minFrames, maxFrames)
			}

			// Overflowing must leave the interpreter usable.
			if got, err := in.Eval(t.Context(), "1 + 1"); err != nil || got != int64(2) {
				t.Errorf("after stack overflow: %#v, %v", got, err)
			}
		})
	}
}

func TestLimitStructsGoThroughJSON(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	if err := in.Exec(ctx, `
def keys(v):
    return sorted(v.keys())

def get(v, k):
    return v[k]
`); err != nil {
		t.Fatal(err)
	}

	type row struct {
		ID     string  `json:"id"`
		Count  int     `json:"count"`
		Ratio  float64 `json:"ratio"`
		Omit   string  `json:"-"`
		hidden string
	}
	arg := row{ID: "r-1", Count: 2, Ratio: 0.5, Omit: "x", hidden: "y"}
	_ = arg.hidden

	// Field names come from the json tags, and the excluded ones vanish:
	// "Omit" is tagged out and "hidden" is unexported.
	got, err := in.Call(ctx, "keys", arg)
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{"count", "id", "ratio"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("struct keys = %#v, want %#v", got, want)
	}

	// The detour does not cost integer-ness: the decoder runs with UseNumber,
	// so a whole number still arrives as a Python int rather than a float.
	for _, tc := range []struct {
		key  string
		want any
	}{
		{"id", "r-1"},
		{"count", int64(2)},
		{"ratio", 0.5},
	} {
		if v, err := in.Call(ctx, "get", arg, tc.key); err != nil || v != tc.want {
			t.Errorf("get(row, %q) = %#v (%T), %v; want %#v", tc.key, v, v, err, tc.want)
		}
	}

	// A map, by contrast, goes direct, so its keys are untouched by json tags.
	direct := map[string]any{"ID": "r-1", "Count": 2}
	if v, err := in.Call(ctx, "keys", direct); err != nil || fmt.Sprint(v) != "[Count ID]" {
		t.Errorf("map keys = %#v, %v; want [Count ID]", v, err)
	}
	if v, err := in.Call(ctx, "get", direct, "Count"); err != nil || v != int64(2) {
		t.Errorf(`get(map, "Count") = %#v (%T), %v; want int64(2)`, v, v, err)
	}
}

func TestLimitUnmarshalableStructIsRejected(t *testing.T) {
	ctx := context.Background()
	in := newT(t)
	if err := in.Exec(ctx, "def echo(v):\n    return v\n"); err != nil {
		t.Fatal(err)
	}

	// A struct json cannot encode is refused rather than silently dropped.
	type bad struct {
		Ch chan int `json:"ch"`
	}
	if _, err := in.Call(ctx, "echo", bad{Ch: make(chan int)}); err == nil {
		t.Fatal("a struct containing a channel was accepted")
	}

	// And (once again) ensure the instance still works.
	if got, err := in.Call(ctx, "echo", "fine"); err != nil || got != "fine" {
		t.Errorf("after the refusal: %#v, %v", got, err)
	}
}
