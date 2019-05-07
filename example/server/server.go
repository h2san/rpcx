package main

import (
	"flag"
	"github.com/h2san/rpcx/protocol/httpx"
	"github.com/h2san/rpcx/protocol/rpcx"
	"github.com/julienschmidt/httprouter"
	"net/http"

	"github.com/h2san/rpcx/example"
	"github.com/h2san/rpcx/server"
)

var (
	addr = flag.String("addr", "localhost:8972", "server address")
)

func main() {
	flag.Parse()

	s := server.NewServer()

	p :=&rpcx.RPCXProtocol{}
	p.Register(new(example.Arith), "")
	s.Protocol = p
	go s.Serve("tcp", "127.0.0.1:8972")

	s2:=server.NewServer()
	p2:=&httpx.HTTProtocol{}
	p2.Route.Handle("GET","/", func(writer http.ResponseWriter, request *http.Request, params httprouter.Params) {
		writer.Write([]byte("ok"))
	})
	s2.Handler = p2
	s2.Serve("http","127.0.0.1:8080")
}
