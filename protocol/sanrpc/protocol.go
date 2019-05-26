package sanrpc

import (
	"context"
	"fmt"
	"github.com/h2san/sanrpc/codec"
	"github.com/h2san/sanrpc/protocol"
	"github.com/pkg/errors"
	"io"
	"reflect"
)

type Protocol struct {
	protocol.BaseService
}

func(p *Protocol)DecodeMessage(r io.Reader) (protocol.Message,error){
	msg := NewMessage()
	err := msg.Decode(r)
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func(p *Protocol)EncodeMessage(msg protocol.Message)[]byte{
	m,ok:=msg.(Message)
	if !ok{
		return []byte{}
	}
	return m.Encode()
}

func(p *Protocol)HandleMessage(ctx context.Context, r protocol.Message) (resp protocol.Message, err error) {
	req,ok := r.(*Message)
	if !ok{
		return nil,errors.New("protocol msg not match")
	}
	serviceName := req.ServicePath
	methodName := req.ServiceMethod

	res := req.Clone()

	res.SetMessageType(Response)

	p.ServiceMapMu.RLock()
	service := p.ServiceMap[serviceName]
	p.ServiceMapMu.RUnlock()

	if service == nil {
		err = errors.New("sanrpc: can't find service " + serviceName)
		return handleError(res, err)
	}
	mtype := service.GetMethod(methodName)
	if mtype == nil {
		if service.GetFunction(methodName) != nil { //check raw functions
			return p.handleRequestForFunction(ctx, req)
		}
		err = errors.New("sanrpc: can't find method " + methodName)
		return handleError(res, err)
	}

	argv :=reflect.New(mtype.ArgType.Elem()).Interface()
	cc := codec.Codecs[req.SerializeType()]
	if cc == nil {
		err = fmt.Errorf("can not find codec for %d", req.SerializeType())
		return handleError(res, err)
	}
	err = cc.Decode(req.Payload, argv)
	if err != nil {
		return handleError(res, err)
	}

	replyv :=reflect.New(mtype.ReplyType.Elem()).Interface()

	if mtype.ArgType.Kind() != reflect.Ptr {
		err = service.Call(ctx, mtype, reflect.ValueOf(argv).Elem(), reflect.ValueOf(replyv))
	} else {
		err = service.Call(ctx, mtype, reflect.ValueOf(argv), reflect.ValueOf(replyv))
	}
	if err != nil {
		return handleError(res, err)
	}
	if !req.IsOneway() {
		data, err := cc.Encode(replyv)
		if err != nil {
			return handleError(res, err)

		}
		res.Payload = data
	}
	return res, nil
}
func (p *Protocol) handleRequestForFunction(ctx context.Context, req *Message) (resp protocol.Message, err error) {
	res := req.Clone()
	res.SetMessageType(Response)

	serviceName := req.ServicePath
	methodName := req.ServiceMethod
	p.ServiceMapMu.RLock()
	service := p.ServiceMap[serviceName]
	p.ServiceMapMu.RUnlock()
	if service == nil {
		err = errors.New("sanrpc: can't find service  for func raw function")
		return handleError(res, err)
	}
	mtype := service.GetFunction(methodName)
	if mtype == nil {
		err = errors.New("sanrpc: can't find method " + methodName)
		return handleError(res, err)
	}

	argv :=reflect.New(mtype.ArgType).Interface()

	cc := codec.Codecs[req.SerializeType()]
	if cc == nil {
		err = fmt.Errorf("can not find codec for %d", req.SerializeType())
		return handleError(res, err)
	}

	err = cc.Decode(req.Payload, argv)
	if err != nil {
		return handleError(res, err)
	}

	replyv :=reflect.New(mtype.ReplyType.Elem()).Interface()

	err = service.CallForFunction(ctx, mtype, reflect.ValueOf(argv), reflect.ValueOf(replyv))

	if err != nil {

		return handleError(res, err)
	}

	if !req.IsOneway() {
		data, err := cc.Encode(replyv)
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