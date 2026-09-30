//go:build e2e

package medium_recipient_hotpath

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	benchtarget "github.com/WuKongIM/WuKongIM/internal/bench/target"
	benchmodel "github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

const permissionSoakStallInterval = 250 * time.Millisecond

type permissionSoakStallSender struct {
	Sender       int     `json:"sender"`
	Ordinal      int     `json:"ordinal"`
	ChannelIndex int     `json:"channel_index"`
	PendingACKs  int     `json:"pending_acks"`
	AgeMS        float64 `json:"age_ms"`
}

type permissionSoakStallSnapshot struct {
	PendingACKs int                         `json:"pending_acks"`
	InvalidKeys int                         `json:"invalid_keys"`
	Senders     []permissionSoakStallSender `json:"senders"`
}

// stallSnapshot is a best-effort concurrent cut, not an atomic cross-session
// snapshot. It retains only the oldest unacknowledged ordinal per sender.
func (t *permissionSoakTracker) stallSnapshot(now time.Time, channelCount int) permissionSoakStallSnapshot {
	var result permissionSoakStallSnapshot
	var senders [mediumSenderConnections]permissionSoakStallSender
	t.starts.Range(func(key, value any) bool {
		start := value.(*permissionSoakMessageStart)
		if start.acked.Load() {
			return true
		}
		raw, ok := key.(string)
		const prefix = "wkrc-permission-soak-"
		if !ok || !strings.HasPrefix(raw, prefix) || len(raw) != len(prefix)+9 || channelCount <= 0 {
			result.InvalidKeys++
			return true
		}
		ordinal, err := strconv.Atoi(strings.TrimPrefix(raw, prefix))
		if err != nil || ordinal <= 0 {
			result.InvalidKeys++
			return true
		}
		result.PendingACKs++
		index := (ordinal - 1) % mediumSenderConnections
		sender := &senders[index]
		sender.PendingACKs++
		age := milliseconds(now.Sub(start.startedAt))
		if age < 0 {
			age = 0
		}
		if sender.Ordinal == 0 || age > sender.AgeMS || (age == sender.AgeMS && ordinal < sender.Ordinal) {
			sender.Sender, sender.Ordinal, sender.ChannelIndex, sender.AgeMS = index, ordinal, (ordinal-1)%channelCount, age
		}
		return true
	})
	for _, sender := range senders {
		if sender.Ordinal != 0 {
			result.Senders = append(result.Senders, sender)
		}
	}
	return result
}

type permissionSoakStallChannel struct {
	ChannelIndex int    `json:"channel_index"`
	Role         string `json:"role"`
	Status       string `json:"status"`
	LEO          uint64 `json:"leo"`
	HW           uint64 `json:"hw"`
	LeaderEpoch  uint32 `json:"leader_epoch"`
	ChannelEpoch uint32 `json:"channel_epoch"`
}

type permissionSoakStallNode struct {
	NodeID     uint64                       `json:"node_id"`
	StartedMS  float64                      `json:"started_ms"`
	DurationMS float64                      `json:"duration_ms"`
	Error      string                       `json:"error,omitempty"`
	Channels   []permissionSoakStallChannel `json:"channels,omitempty"`
}

type permissionSoakStallSample struct {
	OffsetMS float64                     `json:"offset_ms"`
	Pending  permissionSoakStallSnapshot `json:"pending"`
	Nodes    []permissionSoakStallNode   `json:"nodes,omitempty"`
}

type permissionSoakStallEvidence struct {
	Schema            string                      `json:"schema"`
	Samples           int                         `json:"samples"`
	NodeQueries       int                         `json:"node_queries"`
	NodeProbeFailures int                         `json:"node_probe_failures"`
	Recent            []permissionSoakStallSample `json:"recent"`
}

// The observer keeps at most eight seconds of samples. It never blocks the
// load generator and allows only one bounded three-node probe round at a time.
type permissionSoakStallProbe struct {
	mu      sync.Mutex
	ring    [32]permissionSoakStallSample
	count   int
	queries int
	errors  int
	frozen  *permissionSoakStallEvidence
	start   time.Time
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
}

func (p *permissionSoakStallProbe) record(sample permissionSoakStallSample) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen != nil {
		return
	}
	p.ring[p.count%len(p.ring)] = sample
	p.count++
	p.queries += len(sample.Nodes)
	for _, node := range sample.Nodes {
		if node.Error != "" {
			p.errors++
		}
	}
}

func (p *permissionSoakStallProbe) freeze(offsetMS float64) permissionSoakStallEvidence {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen != nil {
		return *p.frozen
	}
	out := permissionSoakStallEvidence{Schema: "wukongim/permission-soak-stall/v1", Samples: p.count, NodeQueries: p.queries, NodeProbeFailures: p.errors}
	first := p.count - len(p.ring)
	if first < 0 {
		first = 0
	}
	for i := first; i < p.count; i++ {
		sample := p.ring[i%len(p.ring)]
		if sample.OffsetMS >= offsetMS-8000 {
			out.Recent = append(out.Recent, sample)
		}
	}
	p.frozen = &out
	return out
}

func startPermissionSoakStallProbe(cluster *suite.StartedCluster, tracker *permissionSoakTracker, channels []string, start time.Time) *permissionSoakStallProbe {
	ctx, cancel := context.WithCancel(context.Background())
	p := &permissionSoakStallProbe{start: start, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(permissionSoakStallInterval)
		defer ticker.Stop()
		first := true
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			now := time.Now()
			pending := tracker.stallSnapshot(now, len(channels))
			sample := permissionSoakStallSample{OffsetMS: milliseconds(now.Sub(start)), Pending: pending}
			query := first && len(pending.Senders) > 0
			for _, sender := range pending.Senders {
				if sender.AgeMS >= 750 {
					query = true
				}
			}
			if query {
				first = false
				request := benchmodel.ChannelRuntimeProbeRequest{}
				for _, sender := range pending.Senders {
					request.Channels = append(request.Channels, benchmodel.ChannelRuntimeChannelIdentity{ChannelID: channels[sender.ChannelIndex], ChannelType: frame.ChannelTypeGroup})
				}
				sample.Nodes = make([]permissionSoakStallNode, len(cluster.Nodes))
				var wg sync.WaitGroup
				for i, node := range cluster.Nodes {
					wg.Add(1)
					go func(i int, node suite.StartedNode) {
						defer wg.Done()
						began := time.Now()
						observation := permissionSoakStallNode{NodeID: node.Spec.ID, StartedMS: milliseconds(began.Sub(start))}
						bounded, stop := context.WithTimeout(ctx, 2*time.Second)
						defer stop()
						client := benchtarget.NewClient(benchtarget.Config{APIAddrs: []string{"http://" + node.APIAddr()}})
						result, err := client.ProbeChannelRuntime(bounded, request)
						observation.DurationMS = milliseconds(time.Since(began))
						if err != nil {
							observation.Error = "probe_failed"
						} else if result.NodeID != node.Spec.ID {
							observation.Error = "node_mismatch"
						} else {
							for j, row := range result.Channels {
								observation.Channels = append(observation.Channels, permissionSoakStallChannel{ChannelIndex: pending.Senders[j].ChannelIndex, Role: row.Role, Status: row.Status, LEO: row.LEO, HW: row.HW, LeaderEpoch: row.LeaderEpoch, ChannelEpoch: row.ChannelEpoch})
							}
						}
						sample.Nodes[i] = observation
					}(i, node)
				}
				wg.Wait()
			}
			p.record(sample)
		}
	}()
	return p
}

func (p *permissionSoakStallProbe) logEvidence(t *testing.T) {
	if p == nil {
		return
	}
	p.once.Do(func() {
		// Freeze the diagnostic at the failure boundary, before HTTP diagnostics
		// and cleanup. Cancel and join an in-progress probe without retaining it.
		out := p.freeze(milliseconds(time.Since(p.start)))
		p.cancel()
		<-p.done
		data, err := json.Marshal(out)
		if err != nil {
			t.Errorf("marshal bounded stall evidence: %v", err)
			return
		}
		t.Logf("WKRC-PERMISSION-SOAK-STALL %s", data)
	})
}
