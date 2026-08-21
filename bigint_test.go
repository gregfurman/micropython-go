package micropython

import (
	"context"
	"math/big"
	"testing"
)

func TestBigIntsDoNotTruncate(t *testing.T) {
	in, _ := NewInstance(context.Background())
	defer in.Close()

	for _, tc := range []struct{ expr, want string }{
		{"1 << 100", "1267650600228229401496703205376"},
		{"(1<<70) + 1", "1180591620717411303425"},
		{"int('9'*30)", "999999999999999999999999999999"},
		{"-(1 << 100)", "-1267650600228229401496703205376"},
		{"2**64", "18446744073709551616"},
		{"2**63 - 1", "9223372036854775807"}, // largest that still fits int64
		{"-(2**63)", "-9223372036854775808"},
	} {
		got, err := in.Eval(t.Context(), tc.expr)
		if err != nil {
			t.Errorf("%s -> %v", tc.expr, err)
			continue
		}

		var text string
		switch v := got.(type) {
		case int64:
			text = big.NewInt(v).String()
		default:
			text = stringOf(v)
		}
		if text != tc.want {
			t.Errorf("%-16s = %v (%T), want %s", tc.expr, got, got, tc.want)
		} else {
			t.Logf("%-16s = %v (%T)", tc.expr, got, got)
		}
	}
}

func stringOf(v any) string {
	type stringer interface{ String() string }
	if s, ok := v.(stringer); ok {
		return s.String()
	}
	return ""
}
