package rpcx

import (
	"context"
	"fmt"
	"github.com/h2san/rpcx/protocol"
	"github.com/h2san/rpcx/share"
	"github.com/pkg/errors"
	"io"
	"reflect"
	"sync"
)

type RPCXProtocol struct {
	serviceMapMu sync.RWMutex
	serviceMap   map[string]*service
}

func(p *RPCXProtocol)DecodeMessage(r io.Reader) (protocol.Message,error){
	msg := NewMessage()
	err := msg.Decode(r)
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func(p *RPCXProtocol)EncodeMessage(msg protocol.Message)[]byte{
	m,ok:=msg.(Message)
	if !ok{
		return []byte{}
	}
	return m.Encode()
}

func(p *RPCXProtocol)HandleMessage(ctx context.Context, r protocol.Message) (resp protocol.Message, err error) {
	req,ok := r.(*Message)
	if !ok{
		return nil,errors.New("protocol msg not match")
	}
	serviceName := req.ServicePath
	methodName := req.ServiceMethod

	res := req.Clone()

	res.SetMessageType(Response)

	p.serviceMapMu.RLock()
	service := p.serviceMap[serviceName]
	p.serviceMapMu.RUnlock()

	if service == nil {
		err = errors.New("rpcx: can't find service " + serviceName)
		return handleError(res, err)
	}
	mtype := service.method[methodName]
	if mtype == nil {
		if service.function[methodName] != nil { //check raw functions
			return p.handleRequestForFunction(ctx, req)
		}
		err = errors.New("rpcx: can't find method " + methodName)
		return handleError(res, err)
	}

	argv :=reflect.New(mtype.ArgType.Elem()).Interface()
	codec := share.Codecs[req.SerializeType()]
	if codec == nil {
		err = fmt.Errorf("can not find codec for %d", req.SerializeType())
		return handleError(res, err)
	}
	err = codec.Decode(req.Payload, argv)
	if err != nil {
		return handleError(res, err)
	}

	replyv :=reflect.New(mtype.ReplyType.Elem()).Interface()

	if mtype.ArgType.Kind() != reflect.Ptr {
		err = service.call(ctx, mtype, reflect.ValueOf(argv).Elem(), reflect.ValueOf(replyv))
	} else {
		err = service.call(ctx, mtype, reflect.ValueOf(argv), reflect.ValueOf(replyv))
	}
	if err != nil {
		return handleError(res, err)
	}
	if !req.IsOneway() {
		data, err := codec.Encode(replyv)
		if err != nil {
			return handleError(res, err)

		}
		res.Payload = data
	}
	return res, nil
}
func (p *RPCXProtocol) handleRequestForFunction(ctx context.Context, req *Message) (resp protocol.Message, err error) {
	res := req.Clone()
	res.SetMessageType(Response)

	serviceName := req.ServicePath
	methodName := req.ServiceMethod
	p.serviceMapMu.RLock()
	service := p.serviceMap[serviceName]
	p.serviceMapMu.RUnlock()
	if service == nil {
		err = errors.New("rpcx: can't find service  for func raw function")
		return handleError(res, err)
	}
	mtype := service.function[methodName]
	if mtype == nil {
		err = errors.New("rpcx: can't find method " + methodName)
		return handleError(res, err)
	}

	argv :=reflect.New(mtype.ArgType).Interface()

	codec := share.Codecs[req.SerializeType()]
	if codec == nil {
		err = fmt.Errorf("can not find codec for %d", req.SerializeType())
		return handleError(res, err)
	}

	err = codec.Decode(req.Payload, argv)
	if err != nil {
		return handleError(res, err)
	}

	replyv :=reflect.New(mtype.ReplyType.Elem()).Interface()

	err = service.callForFunction(ctx, mtype, reflect.ValueOf(argv), reflect.ValueOf(replyv))

	if err != nil {

		return handleError(res, err)
	}

	if !req.IsOneway() {
		data, err := codec.Encode(replyv)
		if err != nil {
			return handleError(res, err)

		}
		res.Payload = data
	}
	return res, nil
}

func handleError(res *Message, err error) (*Message, error) {
	res.SetMessageStatusType(Error)
	if res.Metadata == nil {
		res.Metadata = make(map[string]string)
	}
	res.Metadata[ServiceError] = err.Error()
	return res, err
}