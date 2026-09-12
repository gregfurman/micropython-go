package network

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"strings"
)

// Wraps fs.ErrPermission so a refusal reaches Python as PermissionError rather
// than a bare OSError.
var ErrBlocked = fmt.Errorf("address not allowed: %w", fs.ErrPermission)

type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

type Transport uint8

const (
	TCP Transport = iota
	UDP
)

func (t Transport) String() string {
	if t == UDP {
		return "udp"
	}

	return "tcp"
}

// AnyAddress grants every destination, loopback and private ranges included.
const AnyAddress = "*"

type grant struct {
	transport Transport
	prefix    netip.Prefix // Invalid matches any address.
	port      uint16
}

// AllowList is the set of endpoints Python may reach. Its zero value permits
// nothing, so a list that was never populated cannot open the network.
type AllowList struct {
	grants []grant
}

// Add permits transport to address on port. address is an IP, a CIDR block, or
// AnyAddress; hostnames are not accepted, because connections are authorized
// against the numeric address actually dialled. There is no wildcard port:
// every port is granted deliberately.
func (a *AllowList) Add(transport Transport, address string, port int) error {
	if transport != TCP && transport != UDP {
		return fmt.Errorf("unsupported transport %d", transport)
	}

	if port < 1 || port > 65535 {
		return fmt.Errorf("%s access to %q: port %d is outside 1-65535", transport, address, port)
	}

	prefix, err := parseAddress(address)
	if err != nil {
		return fmt.Errorf("%s access on port %d: %w", transport, port, err)
	}

	a.grants = append(a.grants, grant{transport: transport, prefix: prefix, port: uint16(port)})

	return nil
}

func parseAddress(address string) (netip.Prefix, error) {
	switch address = strings.TrimSpace(address); address {
	case "":
		return netip.Prefix{}, errors.New(`want an IP address, a CIDR block, or "*"`)
	case AnyAddress:
		return netip.Prefix{}, nil
	}

	if strings.Contains(address, "/") {
		prefix, err := netip.ParsePrefix(address)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not a CIDR block", address)
		}

		if prefix.Addr().Is4In6() {
			if prefix.Bits() < 96 {
				return netip.Prefix{}, fmt.Errorf("mapped IPv4 prefix %q must have at least 96 bits", address)
			}

			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}

		return prefix.Masked(), nil
	}

	addr, err := netip.ParseAddr(address)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not an IP address or CIDR block, and hostnames are not supported", address)
	}

	addr = addr.Unmap()
	if addr.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("scoped address %q is unsupported", address)
	}

	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// ValidateIPv4Address rejects grants the current guest ABI cannot represent.
func ValidateIPv4Address(address string) error {
	prefix, err := parseAddress(address)
	if err != nil {
		return err
	}

	if prefix.IsValid() && !prefix.Addr().Is4() {
		return fmt.Errorf("IPv6 address %q is unsupported by the guest", address)
	}

	return nil
}

func (a *AllowList) Allows(transport Transport, ip netip.Addr, port int) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}

	ip = ip.Unmap()

	for _, g := range a.grants {
		if g.transport != transport || int(g.port) != port {
			continue
		}

		if g.prefix.IsValid() && !g.prefix.Contains(ip) {
			continue
		}

		return true
	}

	return false
}

// Dial wraps forward so only allowed endpoints reach it. A nil forward denies
// everything, as does an address that is not numeric or a network this list
// cannot classify.
func (a *AllowList) Dial(forward DialFunc) DialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if forward == nil {
			return nil, ErrBlocked
		}

		transport, ok := transportOf(network)
		if !ok {
			return nil, ErrBlocked
		}

		endpoint, err := netip.ParseAddrPort(address)
		if err != nil {
			return nil, ErrBlocked
		}

		if !a.Allows(transport, endpoint.Addr(), int(endpoint.Port())) {
			return nil, ErrBlocked
		}

		return forward(ctx, network, address)
	}
}

func transportOf(network string) (Transport, bool) {
	switch network {
	case "tcp", "tcp4", "tcp6":
		return TCP, true
	case "udp", "udp4", "udp6":
		return UDP, true
	}

	return 0, false
}

func DenyAll(context.Context, string, string) (net.Conn, error) {
	return nil, ErrBlocked
}
