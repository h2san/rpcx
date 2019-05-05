package main

import (
	"flag"

	example "github.com/h2san/rpcx/example"
	"github.com/h2san/rpcx/server"
)

var (
	addr = flag.String("addr", "localhost:8972", "server address")
)

func main() {
	flag.Parse()

	s := server.NewServer()
	//s.RegisterName("Arith", new(example.Arith), "")
	s.Register(new(example.Arith), "")
	s.Serve("tcp", *addr)
}
