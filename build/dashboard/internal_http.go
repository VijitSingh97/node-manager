package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"syscall"
	"time"

	"google.golang.org/grpc"
)

var errNotInternalAddress = errors.New("connection refused: address is outside the stack network")

func internalDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || !internalNodeHost(host) {
		return errNotInternalAddress
	}
	return nil
}

var internalDialer = &net.Dialer{Timeout: 10 * time.Second, Control: internalDialControl}

func dialInternal(ctx context.Context, address string) (net.Conn, error) {
	return internalDialer.DialContext(ctx, "tcp", address)
}

var internalGRPCDialer = grpc.WithContextDialer(dialInternal)

var internalTransport = &http.Transport{
	Proxy:               nil,
	DialContext:         internalDialer.DialContext,
	MaxIdleConns:        100,
	IdleConnTimeout:     90 * time.Second,
	TLSHandshakeTimeout: 10 * time.Second,
}

// Every client that talks to a stack container is built here; a timeout of 0 leaves the deadline to the caller's context.
func newInternalHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport:     internalTransport,
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
