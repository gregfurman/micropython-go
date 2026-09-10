package network

import (
	"net"
	"net/netip"
	"time"

	"github.com/gregfurman/micropython-go/internal/host/abi"
)

type socket struct {
	kind         int32
	timeout      time.Duration // Negative means blocking without a socket deadline.
	conn         net.Conn
	peer         netip.AddrPort // The numeric destination authorized by DialContext.
	readErr      error
	keepAlive    bool
	keepAliveSet bool
}

func (s *socket) close() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

func (n *Network) get(fd int32) (*socket, int32) {
	s := n.sockets[fd]
	if n.closed || s == nil {
		return nil, -abi.EBADF
	}
	return s, STATUS_OK
}

func (n *Network) add(s *socket) int32 {
	if n.closed {
		return -abi.EBADF
	}
	if len(n.sockets) >= maxSockets || n.next > maxDescriptor {
		return -abi.EMFILE
	}
	if n.sockets == nil {
		n.sockets = make(map[int32]*socket)
	}
	fd := n.next
	n.next++ // Never reuse a token that may survive in guest memory.
	n.sockets[fd] = s
	return fd
}

func (n *Network) delete(fd int32) {
	delete(n.sockets, fd)
}
