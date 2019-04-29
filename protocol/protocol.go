package protocol

import (
	"context"
	"io"

	"github.com/smallnest/rpcx/protocol"
)

const ()

//Message ...
type Message interface {
	Decode(r io.Reader) error
	handleMessage(ctx context.Context, req *protocol.Message) (res *protocol.Message, err error)
	Encode() []byte
}
