// Let the guest make outbound connections. Nothing is reachable by default:
// each address and port is granted explicitly, and name resolution is a grant
// of its own.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"

	micropython "github.com/gregfurman/micropython-go"
)

const src = `
import socket

def get(host, port, path):
    s = socket.socket()
    s.settimeout(5)
    try:
        s.connect(socket.getaddrinfo(host, port)[0][-1])
        s.send(bytes('GET %s HTTP/1.0\r\nHost: %s\r\n\r\n' % (path, host), 'utf8'))
        body = b''
        while True:
            chunk = s.recv(256)
            if not chunk:
                break
            body += chunk
    finally:
        s.close()
    return str(body, 'utf8').split('\r\n\r\n', 1)[1]
`

func main() {
	ctx := context.Background()

	// A real server, so the guest is doing real sockets rather than a stub.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "served %s", r.URL.Path)
	}))
	defer server.Close()

	host, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		log.Fatal(err)
	}
	allowed, err := strconv.Atoi(port)
	if err != nil {
		log.Fatal(err)
	}

	in, err := micropython.NewInstance(ctx,
		micropython.WithSource(src),
		// Grants are additive and specific: this address, this port, TCP only.
		micropython.WithTCPAccess(host, allowed),
		// Resolution is separate. Without it getaddrinfo is denied, even for
		// an address the guest is allowed to connect to.
		micropython.WithDNSResolver(net.DefaultResolver),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer in.Close()

	body, err := in.Call(ctx, "get", host, int64(allowed), "/hello")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(body.Export())

	// A port nobody granted is refused before a socket is opened, with the same
	// EACCES an unreachable host would give, so guest code cannot use refusals
	// to map what is behind the sandbox.
	var exc *micropython.PythonError
	if err := in.Exec(ctx, fmt.Sprintf("get(%q, %d, '/')", host, allowed+1)); !errors.As(err, &exc) {
		log.Fatalf("expected a Python error, got %v", err)
	}
	fmt.Printf("ungranted port: %s %s\n", exc.Type(), exc.Message())
}
