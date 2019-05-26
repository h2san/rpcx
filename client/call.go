package client

import (
	"github.com/h2san/sanrpc/codec"
	"github.com/h2san/sanrpc/log"
)

type Call struct {
	ServicePath   string
	ServiceMethod string
	Metadata      map[string]string
	ResMetadata   map[string]string
	Args          interface{}
	Reply         interface{}
	Error         error
	Done          chan *Call
	Raw           bool

	// 每个调用都可以设置编码与压缩类型
	SerializeType codec.SerializeType
	CompressType  codec.CompressType
}

func (call *Call) done() {
	select {
	case call.Done <- call:
	default:
		log.Debug("rpc: discarding Call reply due to insufficient Done chan capacity")

	}
}
