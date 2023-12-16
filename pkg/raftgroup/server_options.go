package raftgroup

import "time"

type Options struct {
	NodeID            uint64
	Addr              string // 监听地址 格式 ip:port
	Heartbeat         time.Duration
	WorkerCount       int // worker goroutine count
	DataDir           string
	ConnectionManager IConnectionManager
	Transport         ITransport
	TimingWheelTick   time.Duration // The time-round training interval must be 1ms or more
	TimingWheelSize   int64         // Time wheel size

	// ReceiveQueueLength is the length of the receive queue on each worker.
	ReceiveQueueLength uint64
	// MaxReceiveQueueSize is the maximum size in bytes of each receive queue.
	// Once the maximum size is reached, further replication messages will be
	// dropped to restrict memory usage. When set to 0, it means the queue size
	// is unlimited.
	MaxReceiveQueueSize uint64
}

func NewOptions() *Options {
	return &Options{
		Heartbeat:           100 * time.Millisecond,
		WorkerCount:         16,
		TimingWheelTick:     time.Millisecond * 10,
		TimingWheelSize:     100,
		ReceiveQueueLength:  1024,
		MaxReceiveQueueSize: 0,
	}
}

type Option func(*Options)

func WithHeartbeat(heartbeat time.Duration) Option {
	return func(opts *Options) {
		opts.Heartbeat = heartbeat
	}
}

func WithDataDir(dataDir string) Option {
	return func(opts *Options) {
		opts.DataDir = dataDir
	}
}
