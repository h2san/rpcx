// +build !windows

package server

import (
	"net"
)

func init() {
	makeListeners["unix"] = unixMakeListener
}

func unixMakeListener(s *Server, address string) (ln net.Listener, err error) {
	laddr, err := net.ResolveUnixAddr("unix", address)
	if err != nil {
		return nil, err
	}
	return net.ListenUnix("unix", laddr)
}
