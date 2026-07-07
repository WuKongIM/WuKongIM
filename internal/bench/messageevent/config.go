package messageevent

import (
	"fmt"
	"strings"
	"time"
)

// Config describes one black-box message event stream pressure run.
type Config struct {
	// APIAddrs are target HTTP API base addresses used for public writes and metrics reads.
	APIAddrs []string
	// RunID is embedded in generated channel, uid, client message, and event identifiers.
	RunID string
	// Channels is the number of generated group channels.
	Channels int
	// StreamsPerChannel is the number of stream base messages generated per channel.
	StreamsPerChannel int
	// LanesPerStream is the number of event keys updated before each stream finish.
	LanesPerStream int
	// DeltasPerLane is the number of stream.delta updates sent to each event key.
	DeltasPerLane int
	// PayloadBytes is the approximate payload body size for each stream.delta.
	PayloadBytes int
	// Concurrency is the maximum number of streams running at once.
	Concurrency int
	// RequestTimeout bounds each public HTTP API request.
	RequestTimeout time.Duration
	// WarmChannels creates generated channels before measured metrics snapshots.
	WarmChannels bool
	// WarmRuntime sends one normal message per generated channel before measured metrics snapshots.
	WarmRuntime bool
	// ReportDir is the directory where the machine and human-readable reports are written.
	ReportDir string
}

// Shape contains derived counts for a message event stream pressure run.
type Shape struct {
	// Streams is the number of base stream messages generated.
	Streams int `json:"streams"`
	// DeltaEvents is the number of cache-only stream.delta requests sent.
	DeltaEvents int `json:"delta_events"`
	// FinishEvents is the number of stream.finish requests sent.
	FinishEvents int `json:"finish_events"`
	// ExpectedDurableEvents is the compact durable event count expected after finishes.
	ExpectedDurableEvents int `json:"expected_durable_events"`
	// ExpectedFinishProposals is the durable proposal upper bound when every stream finishes once.
	ExpectedFinishProposals int `json:"expected_finish_proposals"`
}

// DefaultConfig returns a small smoke-test shape that can be safely run from a laptop.
func DefaultConfig() Config {
	return Config{
		RunID:             "message-event-stream",
		Channels:          2,
		StreamsPerChannel: 2,
		LanesPerStream:    2,
		DeltasPerLane:     2,
		PayloadBytes:      32,
		Concurrency:       8,
		RequestTimeout:    5 * time.Second,
		ReportDir:         "reports/message-event-stream",
	}
}

// Validate checks static config without contacting the target cluster.
func (c Config) Validate() error {
	if len(c.APIAddrs) == 0 {
		return fmt.Errorf("api addrs: --api is required")
	}
	for i, addr := range c.APIAddrs {
		if strings.TrimSpace(addr) == "" {
			return fmt.Errorf("api addrs[%d] must not be empty", i)
		}
	}
	if c.Channels <= 0 {
		return fmt.Errorf("channels must be greater than zero")
	}
	if c.StreamsPerChannel <= 0 {
		return fmt.Errorf("streams-per-channel must be greater than zero")
	}
	if c.LanesPerStream <= 0 {
		return fmt.Errorf("lanes-per-stream must be greater than zero")
	}
	if c.DeltasPerLane <= 0 {
		return fmt.Errorf("deltas-per-lane must be greater than zero")
	}
	if c.PayloadBytes < 0 {
		return fmt.Errorf("payload-bytes must be greater than or equal to zero")
	}
	if c.Concurrency <= 0 {
		return fmt.Errorf("concurrency must be greater than zero")
	}
	if c.RequestTimeout <= 0 {
		return fmt.Errorf("request-timeout must be greater than zero")
	}
	if strings.TrimSpace(c.ReportDir) == "" {
		return fmt.Errorf("report-dir is required")
	}
	return nil
}

// Shape returns the derived event and proposal counts for the configured run.
func (c Config) Shape() Shape {
	streams := c.Channels * c.StreamsPerChannel
	return Shape{
		Streams:                 streams,
		DeltaEvents:             streams * c.LanesPerStream * c.DeltasPerLane,
		FinishEvents:            streams,
		ExpectedDurableEvents:   streams * (c.LanesPerStream + 1),
		ExpectedFinishProposals: streams,
	}
}
