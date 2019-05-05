package main

import (
	"context"
	"flag"
	"github.com/h2san/rpcx/client"
	"github.com/rpcx-ecosystem/rpcx-examples3"
	"log"
)

var (
	addr = flag.String("addr", "localhost:8972", "server address")
)

func main() {
	flag.Parse()
	o := client.Option{}
	o.SerializeType = 1
	x := client.NewClient(o)
	x.Connect("tcp","127.0.0.1:8080")

	args := &example.Args{
		A: 10,
		B: 20,
	}

	for {
		reply := &example.Reply{}
		err := x.Call(context.Background(), "Mul","", args, reply)
		if err != nil {
			log.Println(err)
			return
		}

	}

}
