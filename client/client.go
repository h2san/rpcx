package client

import (
	"bufio"
	"context"
	"errors"
	"github.com/h2san/sanrpc/codec"
	"github.com/h2san/sanrpc/log"
	"github.com/h2san/sanrpc/protocol/sanrpc"
	"github.com/h2san/sanrpc/share"
	"io"
	"net"
	"sync"
	"time"
)

// ErrShutdown connection is closed.
var (
	ErrShutdown         = errors.New("connection is shut down")
	ErrUnsupportedCodec = errors.New("unsupported codec")
)

// ServiceError is an error from server.
type ServiceError string

func (e ServiceError) Error() string {
	return string(e)
}

type seqKey struct{}

type Client struct {
	option Option
	Conn   net.Conn
	r      *bufio.Reader

	mutex        sync.Mutex
	seq          uint64
	pending      map[uint64]*Call
	closing      bool
	shutdown     bool
	pluginClosed bool

	Plugins PluginContainer
}

func NewClient(option Option) *Client {
	return &Client{
		option: option,
	}
}

func (client *Client) IsClosing() bool {
	return client.closing
}

func (client *Client) IsShutdown() bool {
	return client.shutdown
}
func (client *Client) Go(ctx context.Context, servicePath, serviceMethod string, args interface{}, reply interface{}, done chan *Call) *Call {
	call := new(Call)
	call.ServicePath = servicePath
	call.ServiceMethod = serviceMethod
	meta := ctx.Value(share.ReqMetaDataKey)
	if meta != nil {
		call.Metadata = meta.(map[string]string)
	}
	if _, ok := ctx.(*share.Context); !ok {
		ctx = share.NewContext(ctx)
	}
	call.Args = args
	call.Reply = reply
	call.CompressType = codec.CompressNone
	call.SerializeType = codec.ProtoBuffer

	if compressType, ok :=ctx.Value(share.CallCompressType).(codec.CompressType); ok{
		call.CompressType = compressType
	}
	if serializeType, ok := ctx.Value(share.CallSerializeType).(codec.SerializeType); ok{
		call.SerializeType = serializeType
	}

	if done == nil {
		done = make(chan *Call, 10)
	} else {
		if cap(done) == 0 {
			log.Panic("rpc: done channel is unbuffered")
		}
	}
	call.Done = done
	client.send(ctx, call)
	return call
}

//Call 同步调用
func (client *Client) Call(ctx context.Context, servicePath, serviceMethod string,
	args interface{}, reply interface{}) error {
	if client.Conn == nil {
		return errors.New("conn not establish")
	}

	seq := new(uint64)
	ctx = context.WithValue(ctx, seqKey{}, seq)
	DoneChan := client.Go(ctx, servicePath, serviceMethod, args, reply, make(chan *Call, 1)).Done
	var err error
	select {
	case <-ctx.Done():
		client.mutex.Lock()
		call := client.pending[*seq]
		delete(client.pending, *seq)
		client.mutex.Unlock()
		if call != nil {
			call.Error = ctx.Err()
			call.done()
		}
		return ctx.Err()
	case call := <-DoneChan:
		err = call.Error
		meta := ctx.Value(share.ResMetaDataKey)
		if meta != nil && len(call.ResMetadata) > 0 {
			resMata := meta.(map[string]string)
			for k, v := range call.ResMetadata {
				resMata[k] = v
			}
		}
		return err
	}
}

func (client *Client) send(ctx context.Context, call *Call) {
	client.mutex.Lock()
	if client.shutdown || client.closing {
		call.Error = ErrShutdown
		client.mutex.Unlock()
		call.done()
		return
	}

	cc := codec.Codecs[call.SerializeType]
	if cc == nil {
		call.Error = ErrUnsupportedCodec
		client.mutex.Unlock()
		call.done()
		return
	}
	if client.pending == nil {
		client.pending = make(map[uint64]*Call)
	}
	seq := client.seq
	client.seq++
	client.pending[seq] = call
	client.mutex.Unlock()

	if cseq, ok := ctx.Value(seqKey{}).(*uint64); ok {
		*cseq = seq
	}

	req := sanrpc.Message{}
	req.SetMessageType(sanrpc.Request)
	req.SetSeq(seq)
	if call.Reply == nil {
		req.SetOneway(true)
	}
	if call.ServicePath == "" && call.ServiceMethod == "" {
		req.SetHeartbeat(true)
	} else {
		req.SetSerializeType(call.SerializeType)
		if call.Metadata != nil {
			req.Metadata = call.Metadata
		}
		req.ServicePath = call.ServicePath
		req.ServiceMethod = call.ServiceMethod

		data, err := cc.Encode(call.Args)
		if err != nil {
			call.Error = err
			call.done()
			return
		}
		if len(data) > 1024 && call.CompressType != codec.CompressNone {
			req.SetCompressType(call.CompressType)
		}
		req.Payload = data
	}

	if client.Plugins != nil {
		client.Plugins.DoClientBeforeEncode(req)
	}
	data := req.Encode()

	if client.option.WriteTimeout != 0 {
		_ = client.Conn.SetWriteDeadline(time.Now().Add(client.option.WriteTimeout))
	}
	if client.Conn == nil {
		client.mutex.Lock()
		call = client.pending[seq]
		delete(client.pending, seq)
		client.mutex.Unlock()
		call.Error = errors.New("conn not establish")
		call.done()
		return
	}
	_, err := client.Conn.Write(data)
	if err != nil {
		client.mutex.Lock()
		call = client.pending[seq]
		delete(client.pending, seq)
		client.mutex.Unlock()
		if call != nil {
			call.Error = err
			call.done()
		}
		return
	}
	isOneWay := req.IsOneway()
	if isOneWay {
		client.mutex.Lock()
		call = client.pending[seq]
		delete(client.pending, seq)
		client.mutex.Unlock()
		if call != nil {
			call.done()
		}
	}

}

func (client *Client) input() {
	var err error
	var res = sanrpc.NewMessage()

	for err == nil {
		if client.option.ReadTimeout != 0 {
			_ = client.Conn.SetReadDeadline(time.Now().Add(client.option.ReadTimeout))
		}
		err = res.Decode(client.r)
		if err != nil {
			break
		}
		if client.Plugins != nil {
			client.Plugins.DoClientAfterDecode(res)
		}

		seq := res.Seq()
		var call *Call

		isServerMessage := res.MessageType() == sanrpc.Request && !res.IsHeartbeat() && res.IsOneway()
		if !isServerMessage {
			client.mutex.Lock()
			call = client.pending[seq]
			delete(client.pending, seq)
			client.mutex.Unlock()
		}

		switch {
		case call == nil:
			continue
		case res.MessageStatusType() == sanrpc.Error:
			if len(res.Metadata) > 0 {
				meta := make(map[string]string, len(res.Metadata))
				for k, v := range res.Metadata {
					meta[k] = v
				}
				call.ResMetadata = meta
				call.Error = ServiceError("server error")
			}
			call.done()
		default:
			data := res.Payload
			if len(data) > 0 {
				cc := codec.Codecs[call.SerializeType]
				if cc == nil {
					call.Error = ServiceError(ErrUnsupportedCodec.Error())
				} else {
					err = cc.Decode(data, call.Reply)
					if err != nil {
						call.Error = ServiceError(err.Error())
					}
				}
			}
			if len(res.Metadata) > 0 {
				meta := make(map[string]string, len(res.Metadata))
				for k, v := range res.Metadata {
					meta[k] = v
				}
				call.ResMetadata = res.Metadata
			}
			call.done()
		}
		res.Reset()
	}

	client.mutex.Lock()
	if !client.pluginClosed {
		if client.Plugins != nil {
			client.Plugins.DoClientConnectionClose(client.Conn)
		}
		client.pluginClosed = true
	}
	_ = client.Conn.Close()
	client.shutdown = true
	closing := client.closing
	if err == io.EOF {
		err = ErrShutdown
	} else {
		err = io.ErrUnexpectedEOF
	}
	for _, call := range client.pending {
		call.Error = err
		call.done()
	}
	client.mutex.Unlock()
	if err != nil && err != io.EOF && !closing {
		log.Error("sanrpc: client protocol error:", err)
	}
}

func (client *Client) heartbeat() {
	t := time.NewTicker(client.option.HeartbeatInterval)

	for range t.C {
		if client.shutdown || client.closing {
			t.Stop()
			return
		}

		err := client.Call(context.Background(), "", "", nil, nil)
		if err != nil {
			log.Warnf("failed to heartbeat to %s", client.Conn.RemoteAddr().String())
		}
	}
}

// Close calls the underlying connection's Close method. If the connection is already
// shutting down, ErrShutdown is returned.
func (client *Client) Close() error {
	client.mutex.Lock()

	for seq, call := range client.pending {
		delete(client.pending, seq)
		if call != nil {
			call.Error = ErrShutdown
			call.done()
		}
	}

	var err error
	if !client.pluginClosed {
		if client.Plugins != nil {
			client.Plugins.DoClientConnectionClose(client.Conn)
		}

		client.pluginClosed = true
		err = client.Conn.Close()
	}

	if client.closing || client.shutdown {
		client.mutex.Unlock()
		return ErrShutdown
	}

	client.closing = true
	client.mutex.Unlock()
	return err
}
