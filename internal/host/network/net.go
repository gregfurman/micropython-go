package network

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"time"

	"github.com/gregfurman/micropython-go/internal/host/abi"
	"github.com/gregfurman/micropython-go/internal/host/memory"
	"github.com/gregfurman/micropython-go/internal/micropython"
)

var _ micropython.Xnet = &Network{}

const (
	afInet        = 2
	sockStream    = 1
	sockDgram     = 2
	maxSockets    = 64
	maxDescriptor = 32767 // The guest stores descriptors in a signed 16-bit field.
)

// Network implements the guest's net imports for one interpreter.
// Serialize all methods, including SetContext and Close, with guest execution.
// Cancel the operation context from another goroutine to interrupt blocked I/O.
// Providers must not retain guest buffers or reenter the interpreter.
//
// Supports blocking outbound IPv4 TCP and UDP with at most 64 live sockets.
// Nonblocking I/O, polling, bind, listen, accept, unconnected datagrams, and
// options other than TCP keepalive return EOPNOTSUPP.
type Network struct {
	mem     *memory.Memory
	config  Config
	ctx     context.Context
	sockets map[int32]*socket
	next    int32
	closed  bool
}

// New creates a network host module borrowing mem and owning opened resources.
func New(mem *memory.Memory, config Config) *Network {
	return &Network{mem: mem, config: config, sockets: make(map[int32]*socket)}
}

// SetContext sets the context used by subsequent imports. Nil means Background.
func (n *Network) SetContext(ctx context.Context) { n.ctx = ctx }

func (n *Network) Context() context.Context {
	if n.ctx != nil {
		return n.ctx
	}
	return context.Background()
}

// Close releases every resource and permanently closes this interface.
// Call before abandoning guest memory; do not copy live sockets in snapshots.
func (n *Network) Close() error {
	n.closed = true
	var err error
	for fd, s := range n.sockets {
		n.delete(fd)
		err = errors.Join(err, s.close())
	}
	return err
}

// HasOpenSockets reports whether guest socket handles still exist.
func (n *Network) HasOpenSockets() bool { return len(n.sockets) != 0 }

// Reset closes sockets before a rewind, preserving the descriptor high-water
// mark so stale guest handles cannot refer to newly opened sockets.
func (n *Network) Reset(config Config) error {
	err := n.Close()
	n.config, n.ctx, n.closed = config, nil, false
	return err
}

func (n *Network) Xhost_sock_open(domain, kind int32) int32 {
	if domain != afInet {
		return -abi.EAFNOSUPPORT
	}
	if kind != sockStream && kind != sockDgram {
		return -abi.EOPNOTSUPP
	}
	if err := n.Context().Err(); err != nil {
		return errnoOf(err)
	}
	return n.add(&socket{kind: kind, timeout: -1})
}

func (n *Network) Xhost_sock_close(fd int32) int32 {
	s, status := n.get(fd)
	if status != 0 {
		return status
	}
	n.delete(fd) // Retire the token even if the underlying close fails.
	return errnoOf(s.close())
}

func (n *Network) view(ptr, size int32) ([]byte, int32) {
	if n.mem == nil {
		return nil, -abi.EFAULT
	}
	b, err := n.mem.View(ptr, size)
	if err != nil {
		return nil, -abi.EFAULT
	}
	return b, STATUS_OK
}

func (n *Network) address(ptr, port int32) (netip.AddrPort, int32) {
	if port < 0 || port > 65535 {
		return netip.AddrPort{}, -abi.EINVAL
	}
	ip, status := n.view(ptr, 4)
	if status != 0 {
		return netip.AddrPort{}, status
	}
	return netip.AddrPortFrom(netip.AddrFrom4([4]byte(ip)), uint16(port)), STATUS_OK
}

// Xhost_sock_resolve writes the first IPv4 result; the ABI has room for one.
func (n *Network) Xhost_sock_resolve(ptr, size, ipPtr int32) int32 {
	if n.closed {
		return -abi.EBADF
	}
	name, status := n.view(ptr, size)
	if status != 0 {
		return status
	}
	out, status := n.view(ipPtr, 4)
	if status != 0 {
		return status
	}
	if len(name) == 0 || bytes.IndexByte(name, 0) >= 0 {
		return -abi.EINVAL
	}
	if n.config.LookupIP == nil {
		return -abi.EACCES
	}
	ctx := n.Context()
	if err := ctx.Err(); err != nil {
		return errnoOf(err)
	}
	ips, err := n.config.LookupIP(ctx, "ip4", string(name))
	if ctx.Err() != nil {
		return errnoOf(ctx.Err())
	}
	if err != nil {
		return errnoOf(err)
	}
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			copy(out, ip4)
			return STATUS_OK
		}
	}
	return -abi.EHOSTUNREACH
}

func (n *Network) Xhost_sock_connect(fd, ipPtr, port int32) int32 {
	s, status := n.get(fd)
	if status != 0 {
		return status
	}
	addr, status := n.address(ipPtr, port)
	if status != 0 {
		return status
	}
	if s.conn != nil {
		return -abi.EISCONN
	}
	if n.config.DialContext == nil {
		return -abi.EACCES
	}
	network := "tcp4"
	if s.kind == sockDgram {
		network = "udp4"
	}
	ctx, cancel := n.operation(s)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return errnoOf(err)
	}
	c, err := n.config.DialContext(ctx, network, addr.String())
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		if c != nil {
			c.Close()
		}
		return errnoOf(err)
	}
	if c == nil {
		return -abi.EIO
	}
	if s.keepAliveSet {
		if status := setKeepAlive(c, s.keepAlive); status != 0 {
			c.Close()
			return status
		}
	}
	s.conn, s.peer = c, addr
	return STATUS_OK
}

func (n *Network) Xhost_sock_send(fd, ptr, size int32) int32 {
	s, status := n.get(fd)
	if status != 0 {
		return status
	}
	b, status := n.view(ptr, size)
	if status != 0 {
		return status
	}
	if s.conn == nil {
		return -abi.ENOTCONN
	}
	ctx, cancel := n.operation(s)
	defer cancel()
	finish, err := prepareIO(ctx, s.conn)
	if err != nil {
		return errnoOf(err)
	}
	defer finish()
	count, err := s.conn.Write(b)
	if count == 0 && len(b) != 0 && (err == nil || errors.Is(err, io.EOF)) {
		return -abi.EPIPE
	}
	return ioResult(ctx, count, len(b), err)
}

func (n *Network) Xhost_sock_recv(fd, ptr, size int32) int32 {
	s, status := n.get(fd)
	if status != 0 {
		return status
	}
	b, status := n.view(ptr, size)
	if status != 0 {
		return status
	}
	if s.conn == nil {
		return -abi.ENOTCONN
	}
	if size == 0 && s.kind == sockStream {
		return STATUS_OK
	}
	if s.readErr != nil {
		err := s.readErr
		if !errors.Is(err, io.EOF) {
			s.readErr = nil
		}
		return ioResult(n.Context(), 0, len(b), err)
	}
	ctx, cancel := n.operation(s)
	defer cancel()
	finish, err := prepareIO(ctx, s.conn)
	if err != nil {
		return errnoOf(err)
	}
	defer finish()
	count, err := s.conn.Read(b)
	if count > 0 && count <= len(b) {
		s.readErr = err
	}
	return ioResult(ctx, count, len(b), err)
}

// Serving is out of scope: a listening socket is authorized once and then
// accepts from anyone who can route to it, and accept blocks with no deadline
// while holding the interpreter.
func (n *Network) Xhost_sock_bind(fd, ipPtr, port int32) int32 {
	return n.unsupported(fd)
}

func (n *Network) Xhost_sock_listen(fd, backlog int32) int32 {
	return n.unsupported(fd)
}

func (n *Network) Xhost_sock_accept(fd, ipPtr, portPtr int32) int32 {
	return n.unsupported(fd)
}

// Only the connected peer is permitted: DialContext already authorized it.
func (n *Network) Xhost_sock_sendto(fd, ptr, size, ipPtr, port int32) int32 {
	s, status := n.get(fd)
	if status != 0 {
		return status
	}
	if s.kind != sockDgram || s.conn == nil {
		return -abi.EOPNOTSUPP
	}
	addr, status := n.address(ipPtr, port)
	if status != 0 {
		return status
	}
	if addr != s.peer {
		return -abi.EISCONN
	}
	return n.Xhost_sock_send(fd, ptr, size)
}

func (n *Network) Xhost_sock_recvfrom(fd, ptr, size, ipPtr, portPtr int32) int32 {
	s, status := n.get(fd)
	if status != 0 {
		return status
	}
	if s.kind != sockDgram || s.conn == nil {
		return -abi.EOPNOTSUPP
	}
	ip, status := n.view(ipPtr, 4)
	if status != 0 {
		return status
	}
	port, status := n.view(portPtr, 4)
	if status != 0 {
		return status
	}
	count := n.Xhost_sock_recv(fd, ptr, size)
	if count < 0 {
		return count
	}
	ip4 := s.peer.Addr().As4()
	copy(ip, ip4[:])
	binary.LittleEndian.PutUint32(port, uint32(s.peer.Port()))
	return count
}

func (n *Network) Xhost_sock_poll(fd, flags int32) int32 {
	return n.unsupported(fd) // Do not consume data or guess readiness.
}

func (n *Network) unsupported(fd int32) int32 {
	if _, status := n.get(fd); status != 0 {
		return status
	}
	return -abi.EOPNOTSUPP
}

func (n *Network) Xhost_sock_setsockopt(fd, level, option, value int32) int32 {
	s, status := n.get(fd)
	if status != 0 {
		return status
	}
	// Other options must be configured by the provider before opening sockets.
	if level != 4095 || option != 8 || s.kind != sockStream {
		return -abi.EOPNOTSUPP
	}
	if s.conn != nil {
		if status := setKeepAlive(s.conn, value != 0); status != 0 {
			return status
		}
	}
	s.keepAlive, s.keepAliveSet = value != 0, true
	return STATUS_OK
}

func setKeepAlive(conn net.Conn, enabled bool) int32 {
	c, ok := conn.(interface{ SetKeepAlive(bool) error })
	if !ok {
		return -abi.EOPNOTSUPP
	}
	return errnoOf(c.SetKeepAlive(enabled))
}

func (n *Network) Xhost_sock_settimeout(fd, milliseconds int32) int32 {
	s, status := n.get(fd)
	if status != 0 {
		return status
	}
	if milliseconds < -1 {
		return -abi.EINVAL
	}
	if milliseconds == 0 {
		return -abi.EOPNOTSUPP
	} // No portable nonblocking net.Conn API.
	s.timeout = time.Duration(milliseconds) * time.Millisecond
	return STATUS_OK
}

func (n *Network) operation(s *socket) (context.Context, context.CancelFunc) {
	if s.timeout > 0 {
		return context.WithTimeout(n.Context(), s.timeout)
	}
	return context.WithCancel(n.Context())
}
