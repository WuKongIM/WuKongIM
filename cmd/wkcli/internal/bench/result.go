package bench

import "time"

const (
	resultPass = "pass"
	resultFail = "fail"
)

type latencySummary struct {
	Average time.Duration `json:"avg"`
	P50     time.Duration `json:"p50"`
	P95     time.Duration `json:"p95"`
	P99     time.Duration `json:"p99"`
	Max     time.Duration `json:"max"`
}

type sendResult struct {
	Result     string
	Duration   time.Duration
	Success    uint64
	Errors     uint64
	Throughput float64
	// BandwidthBytes is the measured payload throughput in bytes per second.
	BandwidthBytes float64

	Clients      int
	Gateways     int
	Channels     int
	ChannelPick  string
	Messages     int
	PayloadBytes int
	BatchSize    int

	MinMessagesPerChannel int
	AvgMessagesPerChannel float64
	MaxMessagesPerChannel int

	Latency        latencySummary
	SendackReasons map[string]uint64
	ErrorCounts    map[string]uint64
}
