package client

import (
	"crypto/tls"
	"time"
)
type Option struct {
	Retries        int
	TLSConfig      *tls.Config
	RPCPath        string
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration

	BackupLatency time.Duration
	GenBreaker    func() Breaker

	Heartbeat         bool
	HeartbeatInterval time.Duration
}