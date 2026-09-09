package network

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/gregfurman/micropython-go/internal/host/abi"
	"github.com/gregfurman/micropython-go/internal/host/memory"
)

func newTestNetwork(t *testing.T, config Config) *Network {
	t.Helper()
	n := New(memory.New(1, 2), config)
	t.Cleanup(func() { _ = n.Close() })
	return n
}

func put(t *testing.T, n *Network, ptr int32, data []byte) {
	t.Helper()
	if err := n.mem.Write(ptr, data); err != nil {
		t.Fatal(err)
	}
}

func want(t *testing.T, got, expected int32) {
	t.Helper()
	if got != expected {
		t.Fatalf("got %d, want %d", got, expected)
	}
}

func TestResolve(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "request")
	calls := 0
	n := newTestNetwork(t, Config{LookupIP: func(got context.Context, network, host string) ([]net.IP, error) {
		calls++
		if got.Value(contextKey{}) != "request" || network != "ip4" || host != "service.test" {
			t.Fatalf("lookup: %v, %q, %q", got, network, host)
		}
		return []net.IP{net.ParseIP("::1"), net.ParseIP("192.0.2.1"), net.ParseIP("192.0.2.2")}, nil
	}})
	n.SetContext(ctx)
	put(t, n, 0, []byte("service.test"))
	want(t, n.Xhost_sock_resolve(0, 12, 64), 0)
	ip, _ := n.mem.Read(64, 4)
	if !reflect.DeepEqual(ip, []byte{192, 0, 2, 1}) {
		t.Fatal(ip)
	}
	want(t, n.Xhost_sock_resolve(0, 12, 65535), -abi.EFAULT)
	want(t, n.Xhost_sock_resolve(-1, 12, 64), -abi.EFAULT)
	put(t, n, 0, []byte("bad\x00host"))
	want(t, n.Xhost_sock_resolve(0, 8, 64), -abi.EINVAL)
	if calls != 1 {
		t.Fatalf("unexpected provider calls: %d", calls)
	}
}

func TestResolveFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		lookup func(context.Context, string, string) ([]net.IP, error)
		code   int32
	}{
		{"denied", nil, -abi.EACCES},
		{"empty", func(context.Context, string, string) ([]net.IP, error) { return nil, nil }, -abi.EHOSTUNREACH},
		{"ipv6", func(context.Context, string, string) ([]net.IP, error) { return []net.IP{net.ParseIP("::1")}, nil }, -abi.EHOSTUNREACH},
		{"permission", func(context.Context, string, string) ([]net.IP, error) { return nil, os.ErrPermission }, -abi.EACCES},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newTestNetwork(t, Config{LookupIP: tc.lookup})
			put(t, n, 0, []byte("host"))
			want(t, n.Xhost_sock_resolve(0, 4, 64), tc.code)
		})
	}
}

func TestCapabilitiesAndDescriptors(t *testing.T) {
	n := newTestNetwork(t, Config{})
	want(t, n.Xhost_sock_open(10, sockStream), -abi.EAFNOSUPPORT)
	want(t, n.Xhost_sock_open(afInet, 3), -abi.EOPNOTSUPP)
	fd := n.Xhost_sock_open(afInet, sockStream)
	want(t, fd, 0)
	put(t, n, 64, []byte{192, 0, 2, 1})
	want(t, n.Xhost_sock_connect(fd, 64, 80), -abi.EACCES)
	want(t, n.Xhost_sock_bind(fd, 64, 80), -abi.EOPNOTSUPP)
	want(t, n.Xhost_sock_connect(fd, 65535, 80), -abi.EFAULT)
	want(t, n.Xhost_sock_connect(fd, 64, 65536), -abi.EINVAL)
	want(t, n.Xhost_sock_settimeout(fd, 0), -abi.EOPNOTSUPP)
	want(t, n.Xhost_sock_poll(fd, 1), -abi.EOPNOTSUPP)
	want(t, n.Xhost_sock_settimeout(fd, -2), -abi.EINVAL)
	want(t, n.Xhost_sock_close(fd), 0)
	want(t, n.Xhost_sock_close(fd), -abi.EBADF)
	want(t, n.Xhost_sock_open(afInet, sockStream), 1)
	for len(n.sockets) < maxSockets {
		n.Xhost_sock_open(afInet, sockDgram)
	}
	want(t, n.Xhost_sock_open(afInet, sockStream), -abi.EMFILE)
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	want(t, n.Xhost_sock_open(afInet, sockStream), -abi.EBADF)
	want(t, n.Xhost_sock_resolve(0, 4, 64), -abi.EBADF)
}

type testConn struct {
	net.Conn
	read      func([]byte) (int, error)
	write     func([]byte) (int, error)
	peer      net.Addr
	closes    int
	keepAlive bool
}

func (c *testConn) Read(b []byte) (int, error)      { return c.read(b) }
func (c *testConn) Write(b []byte) (int, error)     { return c.write(b) }
func (c *testConn) RemoteAddr() net.Addr            { return c.peer }
func (c *testConn) SetDeadline(time.Time) error     { return nil }
func (c *testConn) SetKeepAlive(enabled bool) error { c.keepAlive = enabled; return nil }
func (c *testConn) Close() error                    { c.closes++; return nil }

func TestTCPAndPartialRead(t *testing.T) {
	reads := 0
	c := &testConn{
		read: func(b []byte) (int, error) { reads++; return copy(b, "hello"), io.EOF },
		write: func(b []byte) (int, error) {
			if string(b) != "request" {
				t.Fatal(string(b))
			}
			return 3, nil
		},
	}
	n := newTestNetwork(t, Config{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp4" || address != "192.0.2.1:443" {
			t.Fatalf("dial %s %s", network, address)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing socket deadline")
		}
		return c, nil
	}})
	fd := n.Xhost_sock_open(afInet, sockStream)
	put(t, n, 64, []byte{192, 0, 2, 1})
	put(t, n, 128, []byte("request"))
	want(t, n.Xhost_sock_settimeout(fd, 1000), 0)
	want(t, n.Xhost_sock_setsockopt(fd, 4095, 8, 1), 0)
	want(t, n.Xhost_sock_connect(fd, 64, 443), 0)
	if !c.keepAlive {
		t.Fatal("keepalive not applied")
	}
	want(t, n.Xhost_sock_connect(fd, 64, 443), -abi.EISCONN)
	want(t, n.Xhost_sock_send(fd, 128, 7), 3)
	want(t, n.Xhost_sock_recv(fd, 256, 16), 5)
	want(t, n.Xhost_sock_recv(fd, 256, 16), 0)
	want(t, n.Xhost_sock_recv(fd, 256, 16), 0)
	if reads != 1 {
		t.Fatalf("read after terminal EOF: %d", reads)
	}
	b, _ := n.mem.Read(256, 5)
	if string(b) != "hello" {
		t.Fatal(string(b))
	}
	want(t, n.Xhost_sock_close(fd), 0)
	if c.closes != 1 {
		t.Fatalf("closes: %d", c.closes)
	}
}

func TestCancellationAndReuse(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	n := newTestNetwork(t, Config{DialContext: func(context.Context, string, string) (net.Conn, error) { return local, nil }})
	fd := n.Xhost_sock_open(afInet, sockStream)
	put(t, n, 64, []byte{192, 0, 2, 1})
	want(t, n.Xhost_sock_connect(fd, 64, 80), 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n.SetContext(ctx)
	timer := time.AfterFunc(10*time.Millisecond, cancel)
	defer timer.Stop()
	want(t, n.Xhost_sock_recv(fd, 128, 1), -abi.ECANCELED)
	n.SetContext(context.Background())
	done := make(chan error, 1)
	go func() { _, err := remote.Write([]byte("x")); done <- err }()
	want(t, n.Xhost_sock_settimeout(fd, 1000), 0)
	want(t, n.Xhost_sock_recv(fd, 128, 1), 1)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	want(t, n.Xhost_sock_settimeout(fd, 10), 0)
	want(t, n.Xhost_sock_recv(fd, 128, 1), -abi.ETIMEDOUT)
}

func TestCanceledDialClosesReturnedConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &testConn{}
	n := newTestNetwork(t, Config{DialContext: func(context.Context, string, string) (net.Conn, error) {
		cancel()
		return c, nil
	}})
	n.SetContext(ctx)
	fd := n.Xhost_sock_open(afInet, sockStream)
	put(t, n, 64, []byte{192, 0, 2, 1})
	want(t, n.Xhost_sock_connect(fd, 64, 80), -abi.ECANCELED)
	if c.closes != 1 {
		t.Fatalf("closes: %d", c.closes)
	}
	if n.sockets[fd].conn != nil {
		t.Fatal("installed connection after cancellation")
	}
}

// UDP reaches the same dial provider as TCP, so every datagram destination is
// an address the policy saw and allowed.
func TestConnectedUDP(t *testing.T) {
	c := &testConn{
		read:  func(b []byte) (int, error) { return copy(b, "reply"), nil },
		write: func(b []byte) (int, error) { return len(b), nil },
		peer:  &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 53},
	}
	dials := 0
	n := newTestNetwork(t, Config{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		dials++
		if network != "udp4" || address != "192.0.2.1:53" {
			t.Fatalf("dial %s %s", network, address)
		}
		return c, nil
	}})
	fd := n.Xhost_sock_open(afInet, sockDgram)
	put(t, n, 64, []byte{192, 0, 2, 1})
	put(t, n, 128, []byte("query"))
	want(t, n.Xhost_sock_connect(fd, 64, 53), 0)
	want(t, n.Xhost_sock_send(fd, 128, 5), 5)
	want(t, n.Xhost_sock_recv(fd, 256, 16), 5)
	b, _ := n.mem.Read(256, 5)
	if string(b) != "reply" {
		t.Fatal(string(b))
	}
	// Keepalive is a TCP option, and a datagram socket has no use for it.
	want(t, n.Xhost_sock_setsockopt(fd, 4095, 8, 1), -abi.EOPNOTSUPP)
	if dials != 1 {
		t.Fatalf("dials: %d", dials)
	}
}

func TestConnectedUDPSendtoRecvfrom(t *testing.T) {
	reads, writes := 0, 0
	c := &testConn{
		read:  func(b []byte) (int, error) { reads++; return copy(b, "reply"), nil },
		write: func(b []byte) (int, error) { writes++; return len(b), nil },
	}
	n := newTestNetwork(t, Config{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return c, nil
	}})
	fd := n.Xhost_sock_open(afInet, sockDgram)
	put(t, n, 64, []byte{192, 0, 2, 1})
	want(t, n.Xhost_sock_connect(fd, 64, 53), 0)
	put(t, n, 128, []byte("query"))
	want(t, n.Xhost_sock_sendto(fd, 128, 5, 64, 53), 5)
	want(t, n.Xhost_sock_sendto(fd, 128, 0, 64, 53), 0)
	want(t, n.Xhost_sock_sendto(fd, 128, 5, 64, 54), -abi.EISCONN)
	put(t, n, 68, []byte{192, 0, 2, 2})
	want(t, n.Xhost_sock_sendto(fd, 128, 5, 68, 53), -abi.EISCONN)
	want(t, n.Xhost_sock_sendto(fd, 65535, 5, 64, 53), -abi.EFAULT)
	want(t, n.Xhost_sock_recvfrom(fd, 256, 16, 65535, 80), -abi.EFAULT)
	want(t, n.Xhost_sock_recvfrom(fd, 256, 16, 72, 65535), -abi.EFAULT)
	want(t, n.Xhost_sock_recvfrom(fd, 65535, 16, 72, 80), -abi.EFAULT)
	if reads != 0 || writes != 2 {
		t.Fatalf("invalid call performed I/O: reads=%d writes=%d", reads, writes)
	}
	want(t, n.Xhost_sock_recvfrom(fd, 256, 16, 72, 80), 5)
	ip, _ := n.mem.Read(72, 4)
	port, _ := n.mem.Read(80, 4)
	if !reflect.DeepEqual(ip, []byte{192, 0, 2, 1}) || binary.LittleEndian.Uint32(port) != 53 {
		t.Fatalf("wrong peer: %v %v", ip, port)
	}
	want(t, n.Xhost_sock_recvfrom(fd, 256, 0, 72, 80), 0)
	if reads != 2 {
		t.Fatal("zero-size UDP receive did not consume a datagram")
	}
	if err := n.Reset(Config{}); err != nil {
		t.Fatal(err)
	}
	if c.closes != 1 || n.HasOpenSockets() {
		t.Fatal("reset did not close the connection")
	}
	next := n.Xhost_sock_open(afInet, sockDgram)
	if next <= fd {
		t.Fatal("reset reused a stale descriptor")
	}
	want(t, n.Xhost_sock_sendto(fd, 128, 5, 64, 53), -abi.EBADF)
}

// Serving and unconnected datagrams are refused for every socket kind, whatever
// the configuration grants, because no provider authorizes them.
func TestServerOperationsRejected(t *testing.T) {
	n := newTestNetwork(t, Config{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return &testConn{}, nil
	}})
	for _, kind := range []int32{sockStream, sockDgram} {
		fd := n.Xhost_sock_open(afInet, kind)
		put(t, n, 64, []byte{192, 0, 2, 1})
		want(t, n.Xhost_sock_bind(fd, 64, 9000), -abi.EOPNOTSUPP)
		want(t, n.Xhost_sock_listen(fd, 2), -abi.EOPNOTSUPP)
		want(t, n.Xhost_sock_accept(fd, 72, 80), -abi.EOPNOTSUPP)
		want(t, n.Xhost_sock_sendto(fd, 128, 5, 64, 53), -abi.EOPNOTSUPP)
		want(t, n.Xhost_sock_recvfrom(fd, 256, 16, 72, 80), -abi.EOPNOTSUPP)
		want(t, n.Xhost_sock_poll(fd, 1), -abi.EOPNOTSUPP)
		// An unknown descriptor is still reported as such, not as unsupported.
		want(t, n.Xhost_sock_listen(fd+100, 2), -abi.EBADF)
	}
}

func TestIOResultAndErrnos(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int32
	}{
		{nil, 0}, {context.Canceled, -abi.ECANCELED}, {context.DeadlineExceeded, -abi.ETIMEDOUT},
		{os.ErrDeadlineExceeded, -abi.ETIMEDOUT}, {net.ErrClosed, -abi.EBADF},
		{os.ErrPermission, -abi.EACCES}, {os.ErrInvalid, -abi.EINVAL},
		{&net.OpError{Err: syscall.ECONNREFUSED}, -abi.ECONNREFUSED},
		{&net.DNSError{IsNotFound: true}, -abi.EHOSTUNREACH},
		{&net.DNSError{IsTimeout: true}, -abi.ETIMEDOUT},
		{errors.New("unknown"), -abi.EIO},
	} {
		want(t, errnoOf(tc.err), tc.code)
	}
	want(t, ioResult(context.Background(), 5, 4, nil), -abi.EIO)
	want(t, ioResult(context.Background(), -1, 4, nil), -abi.EIO)
	want(t, ioResult(context.Background(), 2, 4, io.EOF), 2)
	want(t, ioResult(context.Background(), 0, 4, io.EOF), 0)
}
