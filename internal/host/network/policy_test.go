package network

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/netip"
	"testing"

	"github.com/gregfurman/micropython-go/internal/host/abi"
)

func TestAllowListAddRejects(t *testing.T) {
	for _, tc := range []struct {
		address string
		port    int
	}{
		{"192.0.2.1", 0},
		{"192.0.2.1", -1},
		{"192.0.2.1", 65536},
		{"", 443},
		{"   ", 443},
		{"api.example.com", 443},
		{"10.0.0.0/33", 443},
		{"192.0.2.1:443", 443}, // The port belongs in the argument, not the address.
	} {
		var list AllowList
		if err := list.Add(TCP, tc.address, tc.port); err == nil {
			t.Errorf("Add(%q, %d) was accepted", tc.address, tc.port)
		}
	}
}

func TestAllowListMatches(t *testing.T) {
	var list AllowList
	for _, g := range []struct {
		transport Transport
		address   string
		port      int
	}{
		{TCP, AnyAddress, 8080},
		{TCP, "192.0.2.7", 9000},
		{TCP, "10.0.0.0/8", 5432},
		{TCP, "2001:db8::/32", 80},
		{UDP, "192.0.2.53", 53},
	} {
		if err := list.Add(g.transport, g.address, g.port); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		transport Transport
		ip        string
		port      int
		allowed   bool
	}{
		{TCP, "192.0.2.1", 8080, true},        // Any address on the granted port.
		{TCP, "192.0.2.1", 443, false},        // A port nobody granted.
		{UDP, "192.0.2.1", 8080, false},       // The grant was TCP only.
		{TCP, "192.0.2.7", 9000, true},        // Exact endpoint.
		{TCP, "192.0.2.8", 9000, false},       // Right port, wrong host.
		{TCP, "10.1.2.3", 5432, true},         // Inside the block.
		{TCP, "11.1.2.3", 5432, false},        // Outside it.
		{TCP, "10.1.2.3", 5433, false},        // Inside the block, ungranted port.
		{TCP, "2001:db8::1", 80, true},        //
		{TCP, "2001:db9::1", 80, false},       //
		{UDP, "192.0.2.53", 53, true},         //
		{TCP, "192.0.2.53", 53, false},        // UDP grants do not carry to TCP.
		{TCP, "::ffff:192.0.2.1", 8080, true}, // A mapped v4 address is still v4.
	} {
		got := list.Allows(tc.transport, netip.MustParseAddr(tc.ip), tc.port)
		if got != tc.allowed {
			t.Errorf("%s %s:%d allowed=%v, want %v", tc.transport, tc.ip, tc.port, got, tc.allowed)
		}
	}

}

func TestAllowListDeniesByDefault(t *testing.T) {
	var list AllowList
	if list.Allows(TCP, netip.MustParseAddr("192.0.2.1"), 8080) {
		t.Fatal("the zero AllowList permitted a connection")
	}
}

func TestMappedIPv4Prefix(t *testing.T) {
	var list AllowList
	if err := list.Add(TCP, "::ffff:192.0.2.0/120", 443); err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"192.0.2.1", "::ffff:192.0.2.1"} {
		if !list.Allows(TCP, netip.MustParseAddr(ip), 443) {
			t.Errorf("mapped grant did not match %s", ip)
		}
	}
	if list.Allows(TCP, netip.MustParseAddr("198.51.100.1"), 443) {
		t.Fatal("mapped grant allowed an unrelated address")
	}
	if err := list.Add(TCP, "::ffff:192.0.2.0/80", 443); err == nil {
		t.Fatal("accepted mapped prefix extending outside IPv4")
	}
	if err := list.Add(Transport(255), AnyAddress, 443); err == nil {
		t.Fatal("accepted invalid transport")
	}
}

func TestAllowListDial(t *testing.T) {
	var list AllowList
	if err := list.Add(TCP, AnyAddress, 8080); err != nil {
		t.Fatal(err)
	}

	dials := 0
	dial := list.Dial(func(context.Context, string, string) (net.Conn, error) {
		dials++
		return &testConn{}, nil
	})

	if _, err := dial(context.Background(), "tcp4", "192.0.2.1:8080"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ network, address string }{
		{"tcp4", "192.0.2.1:443"},        // Ungranted port.
		{"udp4", "192.0.2.1:8080"},       // Granted for TCP only.
		{"unix", "192.0.2.1:8080"},       // Unclassifiable network.
		{"tcp4", "api.example.com:8080"}, // Not numeric.
		{"tcp4", "garbage"},              //
	} {
		if _, err := dial(context.Background(), tc.network, tc.address); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s %s: %v, want ErrBlocked", tc.network, tc.address, err)
		}
	}

	if _, err := list.Dial(nil)(context.Background(), "tcp4", "192.0.2.1:8080"); !errors.Is(err, ErrBlocked) {
		t.Errorf("nil forward: %v, want ErrBlocked", err)
	}

	if dials != 1 {
		t.Fatalf("reached the dialer %d times, want 1", dials)
	}
}

func TestDenyAll(t *testing.T) {
	if _, err := DenyAll(context.Background(), "tcp4", "192.0.2.1:80"); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
}

// A refusal has to arrive in Python as PermissionError, not a bare OSError.
func TestBlockedReachesGuestAsEACCES(t *testing.T) {
	if !errors.Is(ErrBlocked, fs.ErrPermission) {
		t.Fatal("ErrBlocked does not wrap fs.ErrPermission")
	}

	var list AllowList
	if err := list.Add(TCP, AnyAddress, 8080); err != nil {
		t.Fatal(err)
	}

	n := newTestNetwork(t, Config{
		DialContext: list.Dial(func(context.Context, string, string) (net.Conn, error) {
			return &testConn{}, nil
		}),
	})

	fd := n.Xhost_sock_open(afInet, sockStream)
	put(t, n, 64, []byte{192, 0, 2, 1})
	want(t, n.Xhost_sock_connect(fd, 64, 443), -abi.EACCES)
	want(t, n.Xhost_sock_connect(fd, 64, 8080), 0)
}
