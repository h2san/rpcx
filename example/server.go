package main

import (
	"flag"
	"github.com/h2san/rpcx/protocol/rpcx"
	"github.com/h2san/rpcx/server"
)

var (
	addr = flag.String("addr", "localhost:8972", "server address")
)

func main() {
	flag.Parse()

	s := server.NewServer()
	p :=&rpcx.Protocol{}
	
	s.Protocol = p
	s.Serve("tcp", "127.0.0.1:8972")
}
