//go:build e2e

package medium_recipient_hotpath

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

const permissionDriverBinWidth = 100 * time.Millisecond

// permissionSoakDriverProbe measures the load generator, not server receipt.
// Its optional socket wrapper preserves writes and retains no payload or UID.
// A fixed ten-second ring avoids per-message retention during a long soak.
type permissionSoakDriverProbe struct {
	mu      sync.Mutex
	started time.Time
	active  bool
	logged  bool
	totals  permissionDriverCounts
	bins    [100]permissionDriverBin
	frozen  *permissionDriverEvidence
}

type permissionDriverCounts struct {
	SendCalls        uint64  `json:"send_calls"`
	SendErrors       uint64  `json:"send_errors"`
	WriteCalls       uint64  `json:"socket_write_calls"`
	WriteBytes       uint64  `json:"socket_write_bytes"`
	WriteErrors      uint64  `json:"socket_write_errors"`
	MaxScheduleLagMS float64 `json:"max_schedule_lag_ms"`
	MaxEnqueueMS     float64 `json:"max_enqueue_ms"`
	MaxWriteMS       float64 `json:"max_socket_write_ms"`
}

type permissionDriverBin struct {
	permissionDriverCounts
	OffsetMS            int64  `json:"offset_ms"`
	MaxSenderSendCalls  uint64 `json:"max_sender_send_calls"`
	MaxSenderWriteBytes uint64 `json:"max_sender_socket_write_bytes"`
	senderCalls         [mediumSenderConnections]uint64
	senderBytes         [mediumSenderConnections]uint64
}

type permissionDriverEvidence struct {
	permissionDriverCounts
	Schema    string                `json:"schema"`
	ElapsedMS float64               `json:"elapsed_ms"`
	BinMS     int64                 `json:"bin_ms"`
	Recent    []permissionDriverBin `json:"recent"`
}

func (p *permissionSoakDriverProbe) start(at time.Time) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.started = at
	p.active = true
}

// observeEnqueue separates a late scheduled arrival from SendFrame's queue
// admission duration. A successful enqueue does not mean a socket write.
func (p *permissionSoakDriverProbe) observeEnqueue(sender int, target, began, ended time.Time, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active || began.Before(p.started) || ended.Before(began) || sender < 0 || sender >= mediumSenderConnections {
		return
	}
	lag := max(float64(0), milliseconds(began.Sub(target)))
	elapsed := milliseconds(ended.Sub(began))
	record := func(c *permissionDriverCounts) {
		c.SendCalls++
		if err != nil {
			c.SendErrors++
		}
		c.MaxScheduleLagMS = max(c.MaxScheduleLagMS, lag)
		c.MaxEnqueueMS = max(c.MaxEnqueueMS, elapsed)
	}
	record(&p.totals)
	if bin := p.bin(ended); bin != nil {
		record(&bin.permissionDriverCounts)
		bin.senderCalls[sender]++
		bin.MaxSenderSendCalls = max(bin.MaxSenderSendCalls, bin.senderCalls[sender])
	}
}

// observeWrite counts exactly n bytes accepted by the local socket, including
// control frames and partial writes. It does not claim peer receipt or SENDACK.
func (p *permissionSoakDriverProbe) observeWrite(sender int, began, ended time.Time, n int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.active || began.Before(p.started) || ended.Before(began) || sender < 0 || sender >= mediumSenderConnections {
		return
	}
	written := uint64(max(0, n))
	elapsed := milliseconds(ended.Sub(began))
	record := func(c *permissionDriverCounts) {
		c.WriteCalls++
		c.WriteBytes += written
		if err != nil {
			c.WriteErrors++
		}
		c.MaxWriteMS = max(c.MaxWriteMS, elapsed)
	}
	record(&p.totals)
	if bin := p.bin(ended); bin != nil {
		record(&bin.permissionDriverCounts)
		bin.senderBytes[sender] += written
		bin.MaxSenderWriteBytes = max(bin.MaxSenderWriteBytes, bin.senderBytes[sender])
	}
}

// bin requires mu. A delayed observer must not overwrite a newer ring slot.
func (p *permissionSoakDriverProbe) bin(at time.Time) *permissionDriverBin {
	index := int64(at.Sub(p.started) / permissionDriverBinWidth)
	offset := index * permissionDriverBinWidth.Milliseconds()
	bin := &p.bins[index%int64(len(p.bins))]
	if bin.OffsetMS > offset {
		return nil
	}
	if bin.OffsetMS != offset {
		*bin = permissionDriverBin{OffsetMS: offset}
	}
	return bin
}

// freeze seals evidence before failure-report HTTP calls or process cleanup.
// Ring entries are selected by the end timestamp, so an idle tail expires.
func (p *permissionSoakDriverProbe) freeze(at time.Time) permissionDriverEvidence {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen != nil {
		return *p.frozen
	}
	p.active = false
	elapsed := max(time.Duration(0), at.Sub(p.started))
	last := int64(elapsed / permissionDriverBinWidth)
	first := last - int64(len(p.bins)) + 1
	result := permissionDriverEvidence{
		permissionDriverCounts: p.totals,
		Schema:                 "wukongim/permission-soak-driver/v1", ElapsedMS: milliseconds(elapsed),
		BinMS: permissionDriverBinWidth.Milliseconds(), Recent: make([]permissionDriverBin, 0, len(p.bins)),
	}
	for _, bin := range p.bins {
		index := bin.OffsetMS / permissionDriverBinWidth.Milliseconds()
		if bin.SendCalls+bin.WriteCalls > 0 && index >= first && index <= last {
			result.Recent = append(result.Recent, bin)
		}
	}
	sort.Slice(result.Recent, func(i, j int) bool { return result.Recent[i].OffsetMS < result.Recent[j].OffsetMS })
	p.frozen = &result
	return result
}

func (p *permissionSoakDriverProbe) logEvidence(t *testing.T) {
	if p == nil {
		return
	}
	t.Helper()
	p.mu.Lock()
	if p.logged {
		p.mu.Unlock()
		return
	}
	p.logged = true
	p.mu.Unlock()
	encoded, err := json.Marshal(p.freeze(time.Now()))
	if err != nil {
		t.Logf("WKRC-PERMISSION-SOAK-DRIVER marshal error: %v", err)
		return
	}
	t.Logf("WKRC-PERMISSION-SOAK-DRIVER %s", encoded)
}

type permissionSoakDriverDialer struct {
	probe  *permissionSoakDriverProbe
	sender int
}

func (d permissionSoakDriverDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return &permissionSoakDriverConn{Conn: c, probe: d.probe, sender: d.sender, now: time.Now}, nil
}

type permissionSoakDriverConn struct {
	net.Conn
	probe  *permissionSoakDriverProbe
	sender int
	now    func() time.Time
}

func (c *permissionSoakDriverConn) Write(data []byte) (int, error) {
	start := c.now()
	n, err := c.Conn.Write(data)
	c.probe.observeWrite(c.sender, start, c.now(), n, err)
	return n, err
}

func connectPermissionSoakSenders(t *testing.T, cluster *suite.StartedCluster, probe *permissionSoakDriverProbe) []*suite.WKProtoClient {
	t.Helper()
	if probe == nil {
		return connectSenders(t, cluster)
	}
	senders := make([]*suite.WKProtoClient, mediumSenderConnections)
	for index := range senders {
		// Preserve the existing fixture's five-second operation timeout.
		client, err := suite.NewWKProtoClientWithDialer(5*time.Second, permissionSoakDriverDialer{probe: probe, sender: index})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Connect(cluster.MustNode(uint64(index%3+1)).GatewayAddr(), mediumSenderUID(index), fmt.Sprintf("%s-device", mediumSenderUID(index))); err != nil {
			_ = client.Close()
			closeClients(senders[:index])
			t.Fatalf("connect measured sender %d: %v", index, err)
		}
		senders[index] = client
	}
	return senders
}
