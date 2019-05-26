package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"github.com/h2san/sanrpc/protocol/httpx"
	"github.com/h2san/sanrpc/protocol/sanrpc"
	"io"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"os"
	"os/signal"
	"syscall"

	"github.com/h2san/sanrpc/log"
	"github.com/h2san/sanrpc/protocol"
	"github.com/h2san/sanrpc/share"
)

// ErrServerClosed is returned by the Server's Serve, ListenAndServe after a call to Shutdown or Close.
var ErrServerClosed = errors.New("http: Server closed")

const (
	// ReaderBuffsize is used for bufio reader.
	ReaderBuffsize = 1024
	// WriterBuffsize is used for bufio writer.
	WriterBuffsize = 1024
)

// contextKey is a value for use with context.WithValue. It's used as
// a pointer so it fits in an interface{} without allocation.
type contextKey struct {
	name string
}

func (k *contextKey) String() string { return "sanrpc context value " + k.name }

var (
	// RemoteConnContextKey is a context key. It can be used in
	// services with context.WithValue to access the connection arrived on.
	// The associated value will be of type net.Conn.
	RemoteConnContextKey = &contextKey{"remote-conn"}
	// StartRequestContextKey records the start time
	StartRequestContextKey = &contextKey{"start-parse-request"}
	// StartSendRequestContextKey records the start time
	StartSendRequestContextKey = &contextKey{"start-send-request"}
	// TagContextKey is used to record extra info in handling services. Its value is a map[string]interface{}
	TagContextKey = &contextKey{"service-tag"}
)

// Server is sanrpc server that use TCP or UDP.
type Server struct {
	network string
	address string
	ln           net.Listener
	readTimeout  time.Duration
	writeTimeout time.Duration

	mu         sync.RWMutex
	activeConn map[net.Conn]struct{}
	doneChan   chan struct{}
	seq        uint64

	inShutdown int32
	onShutdown []func(s *Server)

	// TLSConfig for creating tls tcp connection.
	tlsConfig *tls.Config

	protocol protocol.MsgProtocol
	httpHandler protocol.HTTPHandlerProtocol

	Plugins PluginContainerer

	handlerMsgNum int32
}

// NewRpcServer returns a server.
func NewRpcServer(options ...OptionFn) *Server {
	s := &Server{
		Plugins: &PluginContainer{},
		protocol:&sanrpc.Protocol{},
	}

	for _, op := range options {
		op(s)
	}

	return s
}

// NewHTTPServer return a http server
func NewHTTPServer(options ...OptionFn) *Server{
	s := &Server{
		Plugins:&PluginContainer{},
		httpHandler:&httpx.HTTProtocol{},
	}
	for _, op := range options{
		op(s)
	}

	return s
}

// Address returns listened address.
func (s *Server) Address() net.Addr {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

// ActiveClientConn returns active connections.
func (s *Server) ActiveClientConn() []net.Conn {
	var result []net.Conn

	for clientConn := range s.activeConn {
		result = append(result, clientConn)
	}
	return result
}

func (s *Server) getDoneChan() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.doneChan == nil {
		s.doneChan = make(chan struct{})
	}
	return s.doneChan
}

func (s *Server) startShutdownListener() {
	go func(s *Server) {
		log.Info("server:", s.network, " address:", s.address, " pid:", os.Getpid())
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGTERM)
		si := <-c
		if si.String() == "terminated" {
			if nil != s.onShutdown && len(s.onShutdown) > 0 {
				for _, sd := range s.onShutdown {
					sd(s)
				}
			}
		}
	}(s)
}

// Serve starts and listens RPC requests.
// It is blocked until receiving connectings from clients.
func (s *Server) Serve(network, address string) (err error) {
	s.network = network
	s.address = address
	s.startShutdownListener()
	var ln net.Listener
	ln, err = s.makeListener(network, address)
	if err != nil {
		log.Errorf("crate listener fail", err)
		return
	}
	if network == "http"{
		return s.serverHTTP(ln)
	}
	return s.serveListener(ln)
}

func (s *Server) serverHTTP(ln net.Listener) error{
	s.ln = ln
	svr := http.Server{
		Handler:s.httpHandler,
	}
	return svr.Serve(ln)
}

// serveListener accepts incoming connections on the Listener ln,
// creating a new service goroutine for each.
// The service goroutines read requests and then call services to reply to them.
func (s *Server) serveListener(ln net.Listener) error {
	if s.protocol == nil {
		s.protocol = &sanrpc.Protocol{}
	}
	if s.Plugins == nil {
		s.Plugins = &PluginContainer{}
	}

	var tempDelay time.Duration

	s.mu.Lock()
	s.ln = ln
	if s.activeConn == nil {
		s.activeConn = make(map[net.Conn]struct{})
	}
	s.mu.Unlock()

	for {
		conn, e := ln.Accept()
		if e != nil {
			select {
			case <-s.getDoneChan():
				return ErrServerClosed
			default:
			}

			if ne, ok := e.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}

				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}

				log.Errorf("sanrpc: Accept error: %v; retrying in %v", e, tempDelay)
				time.Sleep(tempDelay)
				continue
			}
			return e
		}
		tempDelay = 0

		if tc, ok := conn.(*net.TCPConn); ok {
			tc.SetKeepAlive(true)
			tc.SetKeepAlivePeriod(3 * time.Minute)
			tc.SetLinger(10)
		}

		s.mu.Lock()
		s.activeConn[conn] = struct{}{}
		s.mu.Unlock()

		conn, ok := s.Plugins.DoPostConnAccept(conn)
		if !ok {
			continue
		}

		go s.serveConn(conn)
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			ss := runtime.Stack(buf, false)
			if ss > size {
				ss = size
			}
			buf = buf[:ss]
			log.Errorf("serving %s panic error: %s, stack:\n %s", conn.RemoteAddr(), err, buf)
		}
		s.mu.Lock()
		delete(s.activeConn, conn)
		s.mu.Unlock()
		conn.Close()

		if s.Plugins == nil {
			s.Plugins = &PluginContainer{}
		}

		s.Plugins.DoPostConnClose(conn)
	}()

	if isShutdown(s) {
		closeChannel(s, conn)
		return
	}

	if tlsConn, ok := conn.(*tls.Conn); ok {
		if d := s.readTimeout; d != 0 {
			conn.SetReadDeadline(time.Now().Add(d))
		}
		if d := s.writeTimeout; d != 0 {
			conn.SetWriteDeadline(time.Now().Add(d))
		}
		if err := tlsConn.Handshake(); err != nil {
			log.Errorf("sanrpc: TLS handshake error from %s: %v", conn.RemoteAddr(), err)
			return
		}
	}
	ctx := context.Background()
	ctx, cancelCtx := context.WithCancel(ctx)
	connctx := share.WithValue(ctx, "X-SERVER", s)

	in := make(chan protocol.Message, 10)
	out := make(chan protocol.Message, 10)

	r := bufio.NewReaderSize(conn, ReaderBuffsize)

	var wg sync.WaitGroup
	wg.Add(3)

	// 1. read request msg
	{
		go func(ctx *share.Context, in chan<- protocol.Message) {
			defer func() {
				if err := recover(); err != nil {
					const size = 64 << 10
					buf := make([]byte, size)
					ss := runtime.Stack(buf, false)
					if ss > size {
						ss = size
					}
					buf = buf[:ss]
					log.Errorf("serving %s panic error: %s, stack:\n %s", conn.RemoteAddr(), err, buf)
				}
				wg.Done()
				cancelCtx()
			}()

			for {
				if isShutdown(s) {
					closeChannel(s, conn)
					return
				}

				t0 := time.Now()
				if s.readTimeout != 0 {
					conn.SetReadDeadline(t0.Add(s.readTimeout))
				}
				req, err := s.protocol.DecodeMessage(r)
				if err != nil {
					if err == io.EOF {
						log.Infof("client has closed this connection: %s", conn.RemoteAddr().String())
					} else if strings.Contains(err.Error(), "use of closed network connection") {
						log.Infof("sanrpc: connection %s is closed", conn.RemoteAddr().String())
					} else {
						log.Errorf("sanrpc: failed to read request: %v", err)
					}
					return
				}
				select {
				case <-ctx.Done():
					log.Infof("connection: %s read routine context done %v", conn.RemoteAddr().String(),ctx.Err())
					return
				case in <- req:
					log.Infof("read a message from %v", conn.RemoteAddr())
				}
			}
		}(connctx, in)
	}

	// 2. handler request msg
	{
		go func(ctx *share.Context, in <-chan protocol.Message, out chan<- protocol.Message) {
			defer func() {
				if err := recover(); err != nil {
					const size = 64 << 10
					buf := make([]byte, size)
					ss := runtime.Stack(buf, false)
					if ss > size {
						ss = size
					}
					buf = buf[:ss]
					log.Errorf("serving %s panic error: %s, stack:\n %s", conn.RemoteAddr(), err, buf)
				}
				wg.Done()
				cancelCtx()
			}()
			for {
				select {
				case <-ctx.Done():
					log.Infof("connection: %s handle routine context done %v", conn.RemoteAddr().String(),ctx.Err())
					return
				case req := <-in:
					go func() {
						defer func() {
							if err := recover(); err != nil {
								const size = 64 << 10
								buf := make([]byte, size)
								ss := runtime.Stack(buf, false)
								if ss > size {
									ss = size
								}
								buf = buf[:ss]
								log.Errorf("serving %s panic error: %s, stack:\n %s", conn.RemoteAddr(), err, buf)
							}
						}()
						ctx.SetValue("serverr",s)
						resp, err := s.protocol.HandleMessage(ctx, req)
						if err != nil {
							log.Warnf("rpc: failed to handle request: %v", err)
							out <- resp
						}
					}()
				}
			}
		}(connctx, in, out)
	}

	// 3. write response msg
	{
		go func(ctx *share.Context, ch <-chan protocol.Message) {
			defer func() {
				if err := recover(); err != nil {
					const size = 64 << 10
					buf := make([]byte, size)
					ss := runtime.Stack(buf, false)
					if ss > size {
						ss = size
					}
					buf = buf[:ss]
					log.Errorf("serving %s panic error: %s, stack:\n %s", conn.RemoteAddr(), err, buf)
				}
				wg.Done()
				cancelCtx()
			}()

			for{
				select {
				case <- ctx.Done():
					log.Infof("connection: %s write routine context done %v", conn.RemoteAddr().String(),ctx.Err())
					return
				case resp := <- out:
					data := s.protocol.EncodeMessage(resp)
					conn.Write(data)
				}
			}
		}(connctx, out)
	}
	wg.Wait()
	log.Infof("connection %s destroyed", conn.RemoteAddr().String())
}

func isShutdown(s *Server) bool {
	return atomic.LoadInt32(&s.inShutdown) == 1
}

func closeChannel(s *Server, conn net.Conn) {
	s.mu.Lock()
	delete(s.activeConn, conn)
	s.mu.Unlock()
	conn.Close()
}



func (s *Server) auth(ctx context.Context, req *protocol.MsgProtocol) error {

	return nil
}

// Close immediately closes all active net.Listeners.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeDoneChanLocked()
	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}

	for c := range s.activeConn {
		c.Close()
		delete(s.activeConn, c)
		s.Plugins.DoPostConnClose(c)
	}
	return err
}

// RegisterOnShutdown registers a function to call on Shutdown.
// This can be used to gracefully shutdown connections.
func (s *Server) RegisterOnShutdown(f func(s *Server)) {
	s.mu.Lock()
	s.onShutdown = append(s.onShutdown, f)
	s.mu.Unlock()
}

var shutdownPollInterval = 1000 * time.Millisecond

// Shutdown gracefully shuts down the server without interrupting any
// active connections. Shutdown works by first closing the
// listener, then closing all idle connections, and then waiting
// indefinitely for connections to return to idle and then shut down.
// If the provided context expires before the shutdown is complete,
// Shutdown returns the context's error, otherwise it returns any
// error returned from closing the Server's underlying Listener.
func (s *Server) Shutdown(ctx context.Context) error {
	if atomic.CompareAndSwapInt32(&s.inShutdown, 0, 1) {
		log.Info("shutdown begin")
		ticker := time.NewTicker(shutdownPollInterval)
		defer ticker.Stop()
		for {
			if s.checkProcessMsg() {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
		s.Close()
		log.Info("shutdown end")
	}
	return nil
}

func (s *Server) checkProcessMsg() bool {
	size := s.handlerMsgNum
	log.Info("need handle msg size:", size)
	if size == 0 {
		return true
	}
	return false
}

func (s *Server) closeDoneChanLocked() {
	ch := s.getDoneChanLocked()
	select {
	case <-ch:
		// Already closed. Don't close again.
	default:
		// Safe to close here. We're the only closer, guarded
		// by s.mu.
		close(ch)
	}
}
func (s *Server) getDoneChanLocked() chan struct{} {
	if s.doneChan == nil {
		s.doneChan = make(chan struct{})
	}
	return s.doneChan
}

var ip4Reg = regexp.MustCompile(`^(([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])\.){3}([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])$`)

func validIP4(ipAddress string) bool {
	ipAddress = strings.Trim(ipAddress, " ")
	i := strings.LastIndex(ipAddress, ":")
	ipAddress = ipAddress[:i] //remove port

	return ip4Reg.MatchString(ipAddress)
}
