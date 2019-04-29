package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/h2san/rpcx/log"
	"github.com/h2san/rpcx/protocol"
	"github.com/h2san/rpcx/share"
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

const (
	// ReaderBuffsize is used for bufio reader.
	ReaderBuffsize = 16 * 1024
	// WriterBuffsize is used for bufio writer.
	WriterBuffsize = 16 * 1024
)

type RPCClient interface {
	Connect(network, address string) error
	Go(ctx context.Context, servicePath, serviceMethod string, args interface{}, reply interface{}, done chan *Call) *Call
	Call(ctx context.Context, servicePath, serviceMethod string, args interface{}, reply interface{}) error
	SendRaw(ctx context.Context, r *protocol.Message) (map[string]string, []byte, error)
	Close() error

	IsClosing() bool
	IsShutdown() bool
}

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
}

func (call *Call) done() {
	select {
	case call.Done <- call:
	default:
		log.Debug("rpc: discarding Call reply due to insufficient Done chan capacity")

	}
}

type Option struct {
	Group          string
	Retries        int
	TLSConfig      *tls.Config
	RPCPath        string
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration

	BackupLatency time.Duration
	GenBreaker    func() Breaker
	SerializeType protocol.SerializeType
	CompressType  protocol.CompressType

	Heartbeat         bool
	HeartbeatInterval time.Duration
}

type Breaker interface {
	Call(func() error, time.Duration) error
	Fail()
	Success()
	Ready()
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

func (client *Client) Call(ctx context.Context, servicePath, serviceMethod string, args interface{}, reply interface{}) error {

	if client.Conn == nil{
		return errors.New("conn not establish")
	}

	seq := new(uint64)
	ctx = context.WithValue(ctx, seqKey{}, seq)
	Done := client.Go(ctx, servicePath, serviceMethod, args, reply, make(chan *Call, 1)).Done
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
	case call := <-Done:
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

	codec := share.Codecs[client.option.SerializeType]
	if codec == nil {
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

	req := protocol.GetPooledMsg()
	req.SetMessageType(protocol.Request)
	req.SetSeq(seq)
	if call.Reply == nil {
		req.SetOneway(true)
	}
	if call.ServicePath == "" && call.ServiceMethod == "" {
		req.SetHeartbeat(true)
	} else {
		req.SetSerializeType(client.option.SerializeType)
		if call.Metadata != nil {
			req.Metadata = call.Metadata
		}
		req.ServicePath = call.ServicePath
		req.ServiceMethod = call.ServiceMethod

		data, err := codec.Encode(call.Args)
		if err != nil {
			call.Error = err
			call.done()
			return
		}
		if len(data) > 1024 && client.option.CompressType != protocol.None {
			req.SetCompressType(client.option.CompressType)
		}
		req.Payload = data
	}

	if client.Plugins != nil {
		client.Plugins.DoClientBeforeEncode(req)
	}
	data := req.Encode()

	if client.option.WriteTimeout != 0 {
		client.Conn.SetWriteDeadline(time.Now().Add(client.option.WriteTimeout))
	}

	if client.Conn == nil{
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
	isOneway := req.IsOneway()
	protocol.FreeMsg(req)
	if isOneway {
		client.mutex.Lock()
		call = client.pending[seq]
		delete(client.pending, seq)
		client.mutex.Unlock()
		if call != nil {
			call.done()
		}
	}

}

type seqKey struct{}

func (client *Client) input() {
	var err error
	var res = protocol.NewMessage()

	for err == nil {
		if client.option.ReadTimeout != 0 {
			client.Conn.SetReadDeadline(time.Now().Add(client.option.ReadTimeout))
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

		isServerMessage := (res.MessageType() == protocol.Request && !res.IsHeartbeat() && res.IsOneway())
		if !isServerMessage {
			client.mutex.Lock()
			call = client.pending[seq]
			delete(client.pending, seq)
			client.mutex.Unlock()
		}

		switch {
		case call == nil:
			continue
		case res.MessageStatusType() == protocol.Error:
			if len(res.Metadata) > 0 {
				meta := make(map[string]string, len(res.Metadata))
				for k, v := range res.Metadata {
					meta[k] = v
				}
				call.ResMetadata = meta
				call.Error = ServiceError(err.Error())
			}
			call.done()
		default:
			data := res.Payload
			if len(data) > 0 {
				codec := share.Codecs[client.option.SerializeType]
				if codec == nil {
					call.Error = ServiceError(ErrUnsupportedCodec.Error())
				} else {
					err = codec.Decode(data, call.Reply)
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
	client.Conn.Close()
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
		log.Error("rpcx: client protocol error:", err)
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
