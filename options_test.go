package micropython

import (
	"net"
	"testing"
)

// Nil is rejected, but only if it is what the caller last asked for.
func TestDNSResolverLastCallWins(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options []Option
		wantErr bool
	}{
		{"unset", nil, false},
		{"valid", []Option{WithDNSResolver(net.DefaultResolver)}, false},
		{"nil", []Option{WithDNSResolver(nil)}, true},
		{"nil then valid", []Option{WithDNSResolver(nil), WithDNSResolver(net.DefaultResolver)}, false},
		{"valid then nil", []Option{WithDNSResolver(net.DefaultResolver), WithDNSResolver(nil)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := NewInstance(t.Context(), tc.options...)
			if err == nil {
				t.Cleanup(func() { in.Close() })
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("got %v, want an error: %v", err, tc.wantErr)
			}
		})
	}
}
