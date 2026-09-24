package channel

import (
	"context"
	"math"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// MQTTReplayConsumerRequest selects a bounded immutable page covered by an exact
// committed anchor. It confers no Session permission or consumer progress.
type MQTTReplayConsumerRequest struct {
	Request        MQTTReplayRequest
	AnchorPosition uint64
}

func (q MQTTReplayConsumerRequest) Valid() bool {
	r := q.Request
	return r.Valid() && q.AnchorPosition > r.Range.Through && (MQTTReplayPlanRequest{ChannelID: r.ChannelID, ExpectedChannelEpoch: r.ExpectedChannelEpoch, ExpectedLeaderEpoch: r.ExpectedLeaderEpoch, ExpectedRouteGeneration: r.ExpectedRouteGeneration, Generation: r.Range.Generation}).Valid()
}

// MQTTReplayConsumerReader reads through current cluster authority, never using
// ordinary history or a replica-local success as a routing substitute.
type MQTTReplayConsumerReader interface {
	ReadMQTTReplay(context.Context, MQTTReplayConsumerRequest) (MQTTReplayConsumerPage, error)
}

// MQTTReplayPublication owns original message fields and a stable shared-content
// reference. Internal is established by committed native control identities, not
// inferred from SyncOnce or payload bytes. It does not imply delivery permission.
type MQTTReplayPublication struct {
	Message                                                      Message
	Internal                                                     bool
	ContentVersion, AccountedBytes, TotalBytes, TotalStoredBytes uint64
	ContentHash, Digest                                          [32]byte
}

// MQTTReplayConsumerPage preserves source coverage including internal controls.
// It omits canonical storage envelopes while retaining their bounded byte counts.
type MQTTReplayConsumerPage struct {
	Before, After MQTTReplayPrefix
	Records       []MQTTReplayPublication
}

// ValidFor verifies bounded structure and message association. Only the serving
// store proves canonical content, commitment and internal-control classification.
func (p MQTTReplayConsumerPage) ValidFor(id ChannelID, r MQTTReplayRange) bool {
	if id.ID == "" || id.Type == 0 || !r.Valid() || len(p.Records) == 0 || len(p.Records) > r.Limit ||
		p.Before.Generation != r.Generation || p.After.Generation != r.Generation || p.Before.StartAfter != p.After.StartAfter ||
		p.Before.Through != r.From-1 || p.Before.Through < p.Before.StartAfter || p.After.Through < r.From || p.After.Through > r.Through ||
		p.After.Through-p.Before.Through != uint64(len(p.Records)) {
		return false
	}
	if p.Before.Through == p.Before.StartAfter {
		if p.Before.TotalBytes != 0 || p.Before.TotalStoredBytes != 0 || p.Before.Digest != [32]byte{} {
			return false
		}
	} else if p.Before.TotalBytes > p.Before.TotalStoredBytes || p.Before.TotalStoredBytes == 0 || p.Before.Digest == [32]byte{} {
		return false
	}
	remaining, prefix := uint64(r.MaxBytes), p.Before
	for _, e := range p.Records {
		m := e.Message
		if m.MessageID == 0 || m.MessageSeq != prefix.Through+1 || m.ChannelID != id.ID || m.ChannelType != id.Type ||
			m.Version != 0 || m.UpdatedAtMS != 0 || m.TraceID != "" || m.ChannelKey != "" || (e.Internal && !m.SyncOnce) ||
			e.ContentVersion != 1 || e.ContentHash == [32]byte{} || e.Digest == [32]byte{} ||
			e.AccountedBytes != uint64(len(m.Payload))+uint64(len(m.PublicationMetadata)) || math.MaxUint64-prefix.TotalBytes < e.AccountedBytes ||
			e.TotalBytes != prefix.TotalBytes+e.AccountedBytes || e.TotalStoredBytes <= prefix.TotalStoredBytes {
			return false
		}
		size := e.TotalStoredBytes - prefix.TotalStoredBytes
		if size > remaining || e.AccountedBytes > size || uint64(len(m.FromUID))+uint64(len(m.ClientMsgNo))+uint64(len(m.ChannelID)) > size-e.AccountedBytes {
			return false
		}
		if len(m.PublicationMetadata) > 0 {
			metadata, err := publication.Decode(m.PublicationMetadata)
			if err != nil || m.ServerTimestampMS <= 0 {
				return false
			}
			if _, _, err := metadata.ExpiryDeadlineMS(m.ServerTimestampMS); err != nil {
				return false
			}
		}
		remaining -= size
		prefix.Through, prefix.TotalBytes, prefix.TotalStoredBytes, prefix.Digest = m.MessageSeq, e.TotalBytes, e.TotalStoredBytes, e.Digest
	}
	return prefix == p.After
}
