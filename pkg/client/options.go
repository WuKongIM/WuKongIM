package client

import (
	"time"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

// Options Options
type Options struct {
	ProtoVersion   uint8  // 协议版本
	UID            string // 用户uid
	Token          string // 连接IM的token
	AutoReconn     bool   //是否开启自动重连
	DefaultBufSize int    // The size of the bufio reader/writer on top of the socket.

	// ReconnectBufSize is the size of the backing bufio during reconnect.
	// Once this has been exhausted publish operations will return an error.
	// Defaults to 8388608 bytes (8MB).
	ReconnectBufSize int

	// FlusherTimeout is the maximum time to wait for write operations
	// to the underlying connection to complete (including the flusher loop).
	FlusherTimeout time.Duration

	// Timeout sets the timeout for a Dial operation on a connection.
	Timeout time.Duration

	PingInterval time.Duration

	MaxPingCount int // 最大ping的次数

	// ReconnectJitter sets the upper bound for a random delay added to
	// ReconnectWait during a reconnect when no TLS is used.
	ReconnectJitter time.Duration

	// ReconnectWait sets the time to backoff after attempting a reconnect
	// to a server that we were already connected to previously.
	ReconnectWait time.Duration
}

// NewOptions 创建默认配置
func NewOptions() *Options {
	return &Options{
		ProtoVersion:     wkproto.LatestVersion,
		AutoReconn:       false,
		DefaultBufSize:   1024 * 1024 * 3,
		ReconnectBufSize: 8 * 1024 * 1024,
		Timeout:          2 * time.Second,
		PingInterval:     2 * time.Minute,
		MaxPingCount:     2,
		ReconnectJitter:  100 * time.Millisecond,
		ReconnectWait:    2 * time.Second,
	}
}

// Option 参数项
type Option func(*Options) error

// WithProtoVersion 设置协议版本
func WithProtoVersion(version uint8) Option {
	return func(opts *Options) error {
		opts.ProtoVersion = version
		return nil
	}
}

// WithUID 用户UID
func WithUID(uid string) Option {
	return func(opts *Options) error {
		opts.UID = uid
		return nil
	}
}

// WithToken 用户token
func WithToken(token string) Option {
	return func(opts *Options) error {
		opts.Token = token
		return nil
	}
}

// WithAutoReconn WithAutoReconn
func WithAutoReconn(autoReconn bool) Option {
	return func(opts *Options) error {
		opts.AutoReconn = autoReconn
		return nil
	}
}

// SendOptions SendOptions
type SendOptions struct {
	NoPersist   bool // 是否不存储 默认 false
	SyncOnce    bool // 是否同步一次（写模式） 默认 false
	Flush       bool // 是否io flush 默认true
	RedDot      bool // 是否显示红点 默认true
	NoEncrypt   bool // 是否不需要加密
	ClientMsgNo string
}

// NewSendOptions NewSendOptions
func NewSendOptions() *SendOptions {
	return &SendOptions{
		NoPersist: false,
		SyncOnce:  false,
		Flush:     true,
		RedDot:    true,
	}
}

// SendOption 参数项
type SendOption func(*SendOptions) error

// SendOptionWithNoPersist 是否不存储
func SendOptionWithNoPersist(noPersist bool) SendOption {
	return func(opts *SendOptions) error {
		opts.NoPersist = noPersist
		return nil
	}
}

// SendOptionWithSyncOnce 是否只同步一次（写模式）
func SendOptionWithSyncOnce(syncOnce bool) SendOption {
	return func(opts *SendOptions) error {
		opts.SyncOnce = syncOnce
		return nil
	}
}

// SendOptionWithFlush 是否 io flush
func SendOptionWithFlush(flush bool) SendOption {
	return func(opts *SendOptions) error {
		opts.Flush = flush
		return nil
	}
}

// SendOptionWithRedDot 是否显示红点
func SendOptionWithRedDot(redDot bool) SendOption {
	return func(opts *SendOptions) error {
		opts.RedDot = redDot
		return nil
	}
}

// SendOptionWithClientMsgNo 是否显示红点
func SendOptionWithClientMsgNo(clientMsgNo string) SendOption {
	return func(opts *SendOptions) error {
		opts.ClientMsgNo = clientMsgNo
		return nil
	}
}

// SendOptionWithNoEncrypt 是否不需要加密
func SendOptionWithNoEncrypt(noEncrypt bool) SendOption {
	return func(opts *SendOptions) error {
		opts.NoEncrypt = noEncrypt
		return nil
	}
}
