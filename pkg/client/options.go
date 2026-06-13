package client

import (
	"context"
	"net"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	defaultOperationTimeout      = 5 * time.Second
	defaultAckTimeout            = 5 * time.Second
	defaultSendQueueCapacity     = 8192
	defaultMaxInflight           = 8192
	defaultBatchMaxRecords       = 512
	defaultBatchMaxBytes         = 512 * 1024
	defaultBatchMaxWait          = time.Millisecond
	defaultReadBufferSize        = 4096
	defaultInboundFrameBuffer    = 1024
	defaultPoolBalanceRoundRobin = "round_robin"
)

// Dialer opens TCP connections for Client.
type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// Config controls one WKProto client session.
type Config struct {
	// Addr is the target WKProto TCP address.
	Addr string
	// Token is the default CONNECT token when ConnectOptions omits one.
	Token string
	// Dialer opens network connections; nil uses net.Dialer.
	Dialer Dialer
	// OperationTimeout bounds connect, write, and close operations.
	OperationTimeout time.Duration
	// AckTimeout bounds waiting for SENDACK after a SEND is accepted.
	AckTimeout time.Duration
	// SendQueueCapacity is the local outbound SEND queue size.
	SendQueueCapacity int
	// MaxInflight is the maximum number of SENDs waiting for SENDACK.
	MaxInflight int
	// BatchMaxRecords is the maximum SEND frame count per writer batch.
	BatchMaxRecords int
	// BatchMaxBytes is the maximum encoded byte count per writer batch.
	BatchMaxBytes int
	// BatchMaxWait bounds the time spent collecting nearby SEND frames.
	BatchMaxWait time.Duration
	// ReadBufferSize is the socket read buffer size used by the reader loop.
	ReadBufferSize int
	// InboundFrameBufferSize is the inbound RECV queue size.
	InboundFrameBufferSize int
	// AutoRecvAck makes the client automatically acknowledge RECV frames.
	AutoRecvAck bool
	// GenerateClientMsgNo generates ClientMsgNo for messages that omit it.
	GenerateClientMsgNo bool
	// Observer receives optional low-cardinality observations.
	Observer Observer
}

// ConnectOptions controls the WKProto CONNECT packet.
type ConnectOptions struct {
	// UID is the session user id.
	UID string
	// DeviceID is the stable client device id.
	DeviceID string
	// DeviceFlag is the WuKong protocol device category.
	DeviceFlag frame.DeviceFlag
	// Token is the CONNECT token; empty falls back to Config.Token.
	Token string
}

// PoolConfig controls a set of WKProto sessions.
type PoolConfig struct {
	// Addrs is the set of target WKProto TCP addresses.
	Addrs []string
	// Balance selects how sessions are assigned across Addrs.
	Balance string
	// Client is the base config copied into each session.
	Client Config
	// ConnectRatePerSecond limits pool startup connection attempts.
	ConnectRatePerSecond int
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.Addr == "" {
		return Config{}, ErrMissingAddr
	}
	if cfg.Dialer == nil {
		cfg.Dialer = &net.Dialer{}
	}
	if cfg.OperationTimeout <= 0 {
		cfg.OperationTimeout = defaultOperationTimeout
	}
	if cfg.AckTimeout <= 0 {
		cfg.AckTimeout = defaultAckTimeout
	}
	if cfg.SendQueueCapacity <= 0 {
		cfg.SendQueueCapacity = defaultSendQueueCapacity
	}
	if cfg.MaxInflight <= 0 {
		cfg.MaxInflight = defaultMaxInflight
	}
	if cfg.BatchMaxRecords <= 0 {
		cfg.BatchMaxRecords = defaultBatchMaxRecords
	}
	if cfg.BatchMaxBytes <= 0 {
		cfg.BatchMaxBytes = defaultBatchMaxBytes
	}
	if cfg.BatchMaxWait == 0 {
		cfg.BatchMaxWait = defaultBatchMaxWait
	} else if cfg.BatchMaxWait < 0 {
		cfg.BatchMaxWait = 0
	}
	if cfg.ReadBufferSize <= 0 {
		cfg.ReadBufferSize = defaultReadBufferSize
	}
	if cfg.InboundFrameBufferSize <= 0 {
		cfg.InboundFrameBufferSize = defaultInboundFrameBuffer
	}
	return cfg, nil
}
