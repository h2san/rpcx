package protocol

import (
	"context"
	"io"
)

type Message interface {}
//Protocol ...
type MsgProtocol interface {
	DecodeMessage(r io.Reader) (Message,error)
	HandleMessage(ctx context.Context, req Message) (resp Message, err error)
	EncodeMessage(res Message) []byte
}

// SerializeType defines serialization type of payload.
type SerializeType byte

const (
	// SerializeNone uses raw []byte and don't serialize/deserialize
	SerializeNone SerializeType = iota
	// JSON for payload.
	JSON
	// ProtoBuffer for payload.
	ProtoBuffer
)
