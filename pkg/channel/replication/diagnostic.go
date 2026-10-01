// Diagnostic-only source copy for Issue #977. No product repair or qualification.
// Events exist only during an operator-owned runtime trace and have a hard cap.
package replication

import (
	"context"
	"encoding/json"
	"fmt"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"runtime/trace"
	"strconv"
	"strings"
	"sync/atomic"
)

var diagnosticEvents atomic.Uint64

func diagnosticProposal(phase string, manifest ch.ProposalManifest, records []ch.Record, priority uint8, peer uint64, request uint64) {
	if !trace.IsEnabled() {
		return
	}
	ordinal, selected := diagnosticOrdinal(records)
	if !selected {
		return
	}
	n := diagnosticEvents.Add(1)
	if n > 2048 {
		if n == 2049 {
			trace.Log(context.Background(), "wk977.proposal", `{"phase":"cap"}`)
		}
		return
	}
	data, _ := json.Marshal(map[string]any{"phase": phase, "ordinal": ordinal, "cmd": fmt.Sprintf("%x", manifest.CommandID), "digest": fmt.Sprintf("%x", manifest.Digest), "base": manifest.BaseOffset, "last": manifest.LastOffset, "priority": priority, "peer": peer, "request": request})
	trace.Log(context.Background(), "wk977.proposal", string(data))
}
func diagnosticPeer(phase string, node ch.NodeID, priority ExchangePriority, items []queuedPeerItem) {
	for _, item := range items {
		if item.kind == ExchangeReplicate {
			diagnosticProposal(phase, item.replicate.Manifest, item.replicate.Records, uint8(priority), uint64(node), item.requestID)
		}
	}
}

func diagnosticOrdinal(records []ch.Record) (int, bool) {
	for _, record := range records {
		suffix, ok := strings.CutPrefix(record.ClientMsgNo, "two-slots-one-remote-leader-c1-")
		if !ok {
			continue
		}
		ordinal, err := strconv.Atoi(suffix)
		if err == nil && ordinal >= 0 && ordinal < 64 && suffix == fmt.Sprintf("%03d", ordinal) {
			return ordinal, true
		}
	}
	return 0, false
}

var diagnosticBridges atomic.Uint64

// DiagnosticBoundary is an owned-probe observer, disabled outside runtime tracing.
// It emits only a fixed synthetic ordinal and command identity, never payloads.
func DiagnosticBoundary(phase string, command ch.CommandID, records []ch.Record, err error) {
	if !trace.IsEnabled() {
		return
	}
	ordinal, ok := diagnosticOrdinal(records)
	if !ok {
		return
	}
	n := diagnosticBridges.Add(1)
	if n > 2048 {
		if n == 2049 {
			trace.Log(context.Background(), "wk977.bridge", `{"phase":"cap"}`)
		}
		return
	}
	data, _ := json.Marshal(map[string]any{"phase": phase, "ordinal": ordinal, "cmd": fmt.Sprintf("%x", command), "error": err != nil})
	trace.Log(context.Background(), "wk977.bridge", string(data))
}

func diagnosticRound(phase string, p durableProposal, voter ch.NodeID, outcome ch.AppendOutcome, result durableRoundResult, quorum int, err error) {
	if !trace.IsEnabled() {
		return
	}
	ordinal, ok := diagnosticOrdinal(p.records)
	if !ok {
		return
	}
	n := diagnosticEvents.Add(1)
	if n > 2048 {
		if n == 2049 {
			trace.Log(context.Background(), "wk977.proposal", `{"phase":"cap"}`)
		}
		return
	}
	m := p.manifest
	data, _ := json.Marshal(map[string]any{"phase": phase, "ordinal": ordinal, "cmd": fmt.Sprintf("%x", m.CommandID), "digest": fmt.Sprintf("%x", m.Digest), "base": m.BaseOffset, "last": m.LastOffset, "peer": voter, "outcome": uint8(outcome), "durable": outcome.Durable(), "votes": result.durableVotes, "quorum": quorum, "local_durable": result.localDurable, "error": err != nil})
	trace.Log(context.Background(), "wk977.proposal", string(data))
}
