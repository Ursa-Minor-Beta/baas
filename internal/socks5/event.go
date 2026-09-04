package socks5

import (
	"net"

	"golang.org/x/net/proxy"
)

type (
	OnConnectedHandle func(network, address string, port int)
	OnStartedHandle   func(conn *net.TCPListener)
	DialerInitFunc    func(r *Request) proxy.Dialer
)
