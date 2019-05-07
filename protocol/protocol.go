package protocol

import (
	"context"
	"io"
	"net/http"
)

type Message interface {}
//Protocol ...
type MsgProtocol interface {
	DecodeMessage(r io.Reader) (Message,error)
	HandleMessage(ctx context.Context, req Message) (resp Message, err error)
	EncodeMessage(res Message) []byte
}

type HttpHandlerProtocol interface {
	http.Handler
}
