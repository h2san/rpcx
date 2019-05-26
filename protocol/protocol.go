package protocol

import (
	"context"
	"io"
	"net/http"
)

//Message 完整的包体
type Message interface{}

//MsgProtocol rpc protocol inteface
type MsgProtocol interface {
	DecodeMessage(r io.Reader) (Message, error)
	HandleMessage(ctx context.Context, req Message) (resp Message, err error)
	EncodeMessage(res Message) []byte

	RegisterService(service interface{}) error
}

//HTTPHandlerProtocol http protocol interface
type HTTPHandlerProtocol interface {
	ServeHTTP(w http.ResponseWriter,req *http.Request)
	RegisterService(service interface{}) error
	AddPlugin(plugin interface{})
}
