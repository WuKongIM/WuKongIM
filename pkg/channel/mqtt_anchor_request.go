package channel

import (
	"context"
	"encoding/hex"
	"slices"

	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// MQTTReplayAnchorCommitter admits copy evidence through the owning Channel
// sequencer. Entry adapters must establish fresh authority around the call.
type MQTTReplayAnchorCommitter interface {
	CommitMQTTReplayAnchor(context.Context, MQTTReplayAnchorRequest) (MQTTReplayAnchorProof, error)
}

// MQTTReplayAnchorRequest binds a current copy receipt to a server-allocated
// control identity. Exact retries may reuse an earlier control's durable proof.
type MQTTReplayAnchorRequest struct {
	Meta              Meta
	Copy              MQTTReplayCopyReceipt
	MessageID         uint64
	ServerTimestampMS int64
}

// Valid requires explicit current membership and a canonical bounded receipt.
func (q MQTTReplayAnchorRequest) Valid() bool {
	return q.MessageID != 0 && q.ServerTimestampMS > 0 && q.Copy.ValidFor(q.Meta)
}

// Clone owns the mutable slices retained by asynchronous admission.
func (q MQTTReplayAnchorRequest) Clone() MQTTReplayAnchorRequest {
	q.Meta.Replicas = slices.Clone(q.Meta.Replicas)
	q.Meta.ISR = slices.Clone(q.Meta.ISR)
	q.Copy.Copies = slices.Clone(q.Copy.Copies)
	return q
}

// Anchor derives the canonical content checkpoint without trusting payload bytes
// supplied by an entry. Current-copy validity is still required at admission.
func (q MQTTReplayAnchorRequest) Anchor() (quorumlog.MQTTReplayAnchor, error) {
	if !q.Valid() {
		return quorumlog.MQTTReplayAnchor{}, ErrInvalidConfig
	}
	p := q.Copy.After
	a := quorumlog.MQTTReplayAnchor{StartAfter: p.StartAfter, Through: p.Through, TotalBytes: p.TotalBytes, TotalStoredBytes: p.TotalStoredBytes, Digest: p.Digest}
	if _, err := hex.Decode(a.SourceCommand[:], []byte(p.Generation[len("mqtt-log-v1:"):])); err != nil {
		return quorumlog.MQTTReplayAnchor{}, ErrInvalidConfig
	}
	return a, nil
}
