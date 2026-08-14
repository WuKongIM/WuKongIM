package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const (
	// ReceiveDrainStableZeroObservations is the fixed number of separated empty
	// queue cuts required before a worker may claim receive-drain convergence.
	ReceiveDrainStableZeroObservations uint8 = 2
)

// ReceiveDrainSnapshot is a fixed-size, identity-free proof of worker-side
// inbound delivery, RECVACK, and client transport convergence. A blocked
// socket reader is intentionally not work: ReadFramesInFlight counts only
// frames already dequeued and currently being processed.
type ReceiveDrainSnapshot struct {
	// Required reports whether the assignment started background receive drains.
	Required bool `json:"required"`
	// EvidenceComplete reports that every active client exposed the bounded
	// transport queue contract and every aggregate remained representable.
	EvidenceComplete bool `json:"evidence_complete"`
	// DrainComplete reports that the fixed stable-zero condition was observed.
	DrainComplete bool `json:"drain_complete"`
	// ClientCount is the number of clients owned by this receive-drain handle.
	ClientCount uint64 `json:"client_count"`
	// ActiveDrains is the number of clients whose background drain is still live.
	ActiveDrains uint64 `json:"active_drains"`
	// QueueSnapshotClients is the number of clients exposing the bounded
	// WKProto queue snapshot contract.
	QueueSnapshotClients uint64 `json:"queue_snapshot_clients"`
	// InnerRecvDepth is the aggregate pkg/client inbound RECV queue depth.
	InnerRecvDepth uint64 `json:"inner_recv_depth"`
	// InnerRecvHandoffs counts pkg/client frames owned between dequeue and
	// acceptance by the benchmark WKProto adapter.
	InnerRecvHandoffs uint64 `json:"inner_recv_handoffs"`
	// AdapterQueueDepth is the aggregate wkbench WKProto adapter queue depth.
	AdapterQueueDepth uint64 `json:"adapter_queue_depth"`
	// AdapterHandoffs counts adapter RECV, SENDACK, or error results owned
	// between dequeue and acceptance by the matching reader.
	AdapterHandoffs uint64 `json:"adapter_handoffs"`
	// MatchingBufferDepth is the aggregate unmatched-frame buffer depth.
	MatchingBufferDepth uint64 `json:"matching_buffer_depth"`
	// ForegroundMatchers counts explicit SENDACK/RECV matchers that have not returned.
	ForegroundMatchers uint64 `json:"foreground_matchers"`
	// ReadFramesInFlight counts dequeued frames still being classified,
	// buffered, dropped, or acknowledged.
	ReadFramesInFlight uint64 `json:"read_frames_inflight"`
	// RecvACKsInFlight counts protocol receive acknowledgements currently writing.
	RecvACKsInFlight uint64 `json:"recvacks_inflight"`
	// PublicationsInFlight counts SEND results still being published by the
	// WKProto adapter after admission.
	PublicationsInFlight uint64 `json:"publications_inflight"`
	// PublicationWaiters counts SEND callers still blocked on adapter admission.
	PublicationWaiters uint64 `json:"publication_waiters"`
	// RecvACKFailures is the cumulative number of background RECVACK failures.
	RecvACKFailures uint64 `json:"recvack_failures"`
	// RecvACKSuccesses is the cumulative number of protocol RECVACK writes that
	// completed successfully. Auto-ack explicit no-ops do not increment it.
	RecvACKSuccesses uint64 `json:"recvack_successes"`
	// ReadFailures is the cumulative number of non-idle background read failures.
	ReadFailures uint64 `json:"read_failures"`
	// ReceiveFramesObserved counts RECV frames processed by the background or
	// foreground matching path. It invalidates a stable terminal cut when new
	// delivery arrives before sessions close.
	ReceiveFramesObserved uint64 `json:"receive_frames_observed"`
	// BufferedFramesDrained counts already-acknowledged or obsolete unmatched
	// frames consumed at the terminal receive boundary.
	BufferedFramesDrained uint64 `json:"buffered_frames_drained"`
	// FanoutProof is the fixed-size, identity-free multiset witness that binds
	// successful logical group SENDACKs to the exact recipient RECV and
	// successful RECVACK occurrences. It is populated by reviewed group runs;
	// queue convergence remains a separate transport invariant.
	FanoutProof FanoutProofSnapshot `json:"fanout_proof"`
	// StableZeroObservations counts consecutive separated healthy zero-work cuts,
	// capped at ReceiveDrainStableZeroObservations.
	StableZeroObservations uint8 `json:"stable_zero_observations"`
}

// PendingWork reports whether any bounded worker/client queue or processing
// stage still owns receive or publication work.
func (s ReceiveDrainSnapshot) PendingWork() bool {
	return s.InnerRecvDepth != 0 || s.InnerRecvHandoffs != 0 ||
		s.AdapterQueueDepth != 0 || s.AdapterHandoffs != 0 ||
		s.MatchingBufferDepth != 0 || s.ForegroundMatchers != 0 ||
		s.ReadFramesInFlight != 0 ||
		s.RecvACKsInFlight != 0 || s.PublicationsInFlight != 0 ||
		s.PublicationWaiters != 0
}

// FailureFree reports whether receive processing has remained error-free.
func (s ReceiveDrainSnapshot) FailureFree() bool {
	return s.RecvACKFailures == 0 && s.ReadFailures == 0
}

// ZeroCutComplete reports whether one point-in-time snapshot is a complete,
// healthy zero-work cut. It does not by itself establish temporal stability.
func (s ReceiveDrainSnapshot) ZeroCutComplete() bool {
	if !s.EvidenceComplete || !s.FailureFree() || s.PendingWork() {
		return false
	}
	if !s.Required {
		return s.ClientCount == 0 && s.ActiveDrains == 0 && s.QueueSnapshotClients == 0
	}
	return s.ClientCount > 0 && s.ActiveDrains == s.ClientCount &&
		s.QueueSnapshotClients == s.ClientCount
}

// TerminalProofComplete reports whether this snapshot can participate in a
// terminal_pre_close worker proof.
func (s ReceiveDrainSnapshot) TerminalProofComplete() bool {
	if !s.Required {
		return s == ReceiveDrainNotRequired()
	}
	return s.DrainComplete &&
		s.StableZeroObservations >= ReceiveDrainStableZeroObservations &&
		s.ZeroCutComplete()
}

// ReceiveDrainNotRequired returns the canonical complete proof for an
// assignment with no receive-drain clients.
func ReceiveDrainNotRequired() ReceiveDrainSnapshot {
	return ReceiveDrainSnapshot{
		EvidenceComplete:       true,
		DrainComplete:          true,
		FanoutProof:            FanoutProofNotRequired(),
		StableZeroObservations: ReceiveDrainStableZeroObservations,
	}
}

// ReceiveDrainFingerprint returns the canonical low-cardinality digest bound
// to a terminal product cut. Any late delivery, queue ownership, failure, or
// stable-zero generation change produces a different digest.
func ReceiveDrainFingerprint(snapshot ReceiveDrainSnapshot) string {
	body, err := json.Marshal(snapshot)
	if err != nil {
		panic("marshal receive drain snapshot: " + err.Error())
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
