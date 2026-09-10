package network

import (
	"context"
	"net"
)

// Config supplies networking capabilities. Nil functions deny access; there is
// no fallback to the process resolver or dialer. Providers must obey ctx and
// authorize their requests.
//
// Outbound only. There is no listener provider, because a listening socket is
// authorized once at creation and then accepts from anyone who can reach it,
// while a dial is authorized per destination. Guest code that needs to serve
// traffic belongs in Go, handing work inward through a host function.
//
// DialContext covers UDP as well as TCP: the guest connects a SOCK_DGRAM
// socket and sends on it, so every datagram destination is still an address
// this provider saw and allowed.
type Config struct {
	LookupIP    func(ctx context.Context, network, host string) ([]net.IP, error)
	DialContext func(ctx context.Context, network, address string) (net.Conn, error)
}
