package micropython

import (
	"strings"
	"testing"
)

// A script that prints and then raises must hand back both.
func TestExecOutputSurvivesError(t *testing.T) {
	in := newT(t)

	out, err := in.Exec(t.Context(), "print('before')\nraise ValueError('boom')\n")
	if err == nil {
		t.Fatal("expected an error")
	}
	if out != "before\n" {
		t.Errorf("out = %q, want %q", out, "before\n")
	}
	if !strings.Contains(err.Error(), "ValueError") {
		t.Errorf("err = %v", err)
	}
}
