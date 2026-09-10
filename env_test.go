package micropython

import (
	"strings"
	"testing"
)

func TestEnvReachesOSGetenv(t *testing.T) {
	in, err := NewInstance(t.Context(), WithEnv("STAGE", "prod"), WithEnv("EMPTY", ""))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close() })

	for _, tc := range []struct {
		src  string
		want any
	}{
		{`os.getenv('STAGE')`, "prod"},
		{`os.getenv('EMPTY')`, ""},
		{`repr(os.getenv('NOPE'))`, "None"},
		{`os.getenv('NOPE', 'fallback')`, "fallback"},

		// The guest can set its own, and they are gone once unset.
		{`(os.putenv('K', 'v'), os.getenv('K'))[1]`, "v"},
		{`(os.unsetenv('K'), repr(os.getenv('K')))[1]`, "None"},

		// Unsetting a name that was never set is not an error.
		{`repr(os.unsetenv('NEVER'))`, "None"},

		// A value past the guest's inline buffer takes a second host call to
		// size and fetch, which must return the whole thing.
		{`(os.putenv('BIG', 'x' * 5000), len(os.getenv('BIG')))[1]`, int64(5000)},

		// The Go process environment is never a source for any of this.
		{`repr(os.getenv('PATH'))`, "None"},
		{`repr(os.getenv('HOME'))`, "None"},
	} {
		if err := in.Exec(t.Context(), "import os"); err != nil {
			t.Fatal(err)
		}
		got, err := in.Eval(t.Context(), tc.src)
		if err != nil {
			t.Errorf("%s: %v", tc.src, err)
			continue
		}
		if got.Export() != tc.want {
			t.Errorf("%s = %#v, want %#v", tc.src, got.Export(), tc.want)
		}
	}
}

func TestEnvRejectedAtConstruction(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"", "v"},
		{"A=B", "v"},
		{"A\x00B", "v"},
		{"A", "v\x00w"},
		{strings.Repeat("n", 1025), "v"},
		{"A", strings.Repeat("v", 65537)},
	} {
		in, err := NewInstance(t.Context(), WithEnv(tc.name, tc.value))
		if err == nil {
			in.Close()
			t.Errorf("WithEnv(%q, %q) was accepted", tc.name, tc.value)
			continue
		}
		if !strings.Contains(err.Error(), "environment variable") {
			t.Errorf("WithEnv(%q, %q): %v", tc.name, tc.value, err)
		}
	}

	// A later valid call does not excuse an earlier bad one.
	if in, err := NewInstance(t.Context(), WithEnv("", "v"), WithEnv("OK", "v")); err == nil {
		in.Close()
		t.Error("a bad variable was excused by a good one")
	}

	// Programs validate the same way.
	if p, err := NewProgram(t.Context(), WithEnv("A=B", "v")); err == nil {
		p.Close()
		t.Error("Compile accepted a bad variable")
	}
}

func TestEnvRejectsMalformedNames(t *testing.T) {
	in := newT(t)
	if err := in.Exec(t.Context(), "import os"); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{
		`os.getenv('')`,
		`os.putenv('', 'v')`,
		`os.putenv('A=B', 'v')`,
		`os.putenv('A\x00B', 'v')`,
		`os.putenv('A', 'v\x00w')`,
		`os.unsetenv('A=B')`,
	} {
		if got := raises(t, in, src).Type(); got != "OSError" {
			t.Errorf("%s raised %s, want OSError", src, got)
		}
	}
}

// Rewind restores initialization-time variables, including Python's changes.
func TestEnvIsRewoundWithTheInstance(t *testing.T) {
	program, err := NewProgram(t.Context(), WithSource(`
import os
os.putenv('STAGE', 'compiled')
`), WithEnv("STAGE", "configured"), WithMaxIdle(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { program.Close() })

	for range 2 {
		if err := program.Run(t.Context(), func(in *BorrowedInstance) error {
			return in.Exec(t.Context(), `
assert os.getenv('STAGE') == 'compiled'
assert os.getenv('ADDED') is None
os.putenv('STAGE', 'run')
os.putenv('ADDED', 'run')
`)
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEnvCloneCopiesCurrentVariables(t *testing.T) {
	in, err := NewInstance(t.Context(), WithEnv("STAGE", "configured"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close() })
	if err := in.Exec(t.Context(), "import os; os.putenv('STAGE', 'snapshot')"); err != nil {
		t.Fatal(err)
	}
	clone, err := in.Clone(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clone.Close() })
	if err := clone.Exec(t.Context(), "assert os.getenv('STAGE') == 'snapshot'; os.putenv('STAGE', 'clone')"); err != nil {
		t.Fatal(err)
	}
	if err := in.Exec(t.Context(), "assert os.getenv('STAGE') == 'snapshot'"); err != nil {
		t.Fatal(err)
	}
}
