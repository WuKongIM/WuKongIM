package channel

import (
	"context"
	"encoding/hex"
	"math"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// MQTTReplayPreparer prepares local shared content on a recovered leader. The
// caller must establish fresh routing authority; the result is no quorum receipt.
type MQTTReplayPreparer interface {
	PrepareMQTTReplay(context.Context, MQTTReplayRequest) (MQTTReplayPage, error)
}

// MQTTReplayRequest preserves the caller's exact admission fences and range.
type MQTTReplayRequest struct {
	ChannelID ChannelID
	// Expected fences identify one caller-selected authority; preparation never refreshes them.
	ExpectedChannelEpoch, ExpectedLeaderEpoch, ExpectedRouteGeneration uint64
	Range                                                              MQTTReplayRange
}

// MQTTReplayRange bounds one contiguous page of the protected log incarnation.
type MQTTReplayRange struct {
	Generation      string
	From, Through   uint64
	Limit, MaxBytes int
}

// Valid requires the canonical activated generation and explicit finite page budgets.
func (r MQTTReplayRange) Valid() bool {
	const prefix = "mqtt-log-v1:"
	var command CommandID
	if len(r.Generation) != len(prefix)+hex.EncodedLen(len(command)) || !strings.HasPrefix(r.Generation, prefix) {
		return false
	}
	if _, err := hex.Decode(command[:], []byte(r.Generation[len(prefix):])); err != nil || command == (CommandID{}) || r.Generation != quorumlog.MQTTSourceGeneration(command) {
		return false
	}
	return r.From > 0 && r.Through >= r.From && r.Limit > 0 && r.Limit <= 256 && r.MaxBytes > 0 && r.MaxBytes <= 16<<20
}

// Valid rejects implicit authority and unbounded or empty ranges before admission.
func (r MQTTReplayRequest) Valid() bool {
	return r.ChannelID.ID != "" && r.ChannelID.Type != 0 && r.ExpectedChannelEpoch != 0 &&
		r.ExpectedLeaderEpoch != 0 && r.ExpectedRouteGeneration != 0 && r.Range.Valid()
}

// MQTTReplayPrefix identifies complete immutable local coverage, not authority.
type MQTTReplayPrefix struct {
	Generation                   string
	StartAfter, Through          uint64
	TotalBytes, TotalStoredBytes uint64
	Digest                       [32]byte
}

// MQTTReplayRecord owns the opaque canonical original-row envelope, retaining
// native fields independently of entry-protocol decoding or per-session storage.
type MQTTReplayRecord struct {
	Position, ContentVersion, MessageID          uint64
	AccountedBytes, TotalBytes, TotalStoredBytes uint64
	ContentHash, Digest                          [32]byte
	Content                                      []byte
}

// MQTTReplayPage covers (Before.Through, After.Through] and owns its row bytes.
type MQTTReplayPage struct {
	Before, After MQTTReplayPrefix
	Records       []MQTTReplayRecord
}

// ValidFor performs bounded structural validation only. Storage validates row
// codecs and full-content hashes; current runtime authority is checked separately.
func (p MQTTReplayPage) ValidFor(r MQTTReplayRange) bool {
	if !r.Valid() || len(p.Records) == 0 || len(p.Records) > r.Limit || p.Before.Generation != r.Generation || p.After.Generation != r.Generation ||
		p.Before.StartAfter != p.After.StartAfter || p.Before.Through != r.From-1 || p.Before.Through < p.Before.StartAfter ||
		p.After.Through < r.From || p.After.Through > r.Through || p.After.Through-p.Before.Through != uint64(len(p.Records)) {
		return false
	}
	if p.Before.Through == p.Before.StartAfter {
		if p.Before.TotalBytes != 0 || p.Before.TotalStoredBytes != 0 || p.Before.Digest != [32]byte{} {
			return false
		}
	} else if p.Before.TotalBytes > p.Before.TotalStoredBytes || p.Before.TotalStoredBytes == 0 || p.Before.Digest == [32]byte{} {
		return false
	}
	remaining, prefix := r.MaxBytes, p.Before
	for _, entry := range p.Records {
		if entry.Position != prefix.Through+1 || entry.ContentVersion != 1 || entry.MessageID == 0 || len(entry.Content) == 0 || len(entry.Content) > remaining ||
			entry.ContentHash == [32]byte{} || entry.Digest == [32]byte{} || entry.AccountedBytes > uint64(len(entry.Content)) ||
			math.MaxUint64-prefix.TotalBytes < entry.AccountedBytes || math.MaxUint64-prefix.TotalStoredBytes < uint64(len(entry.Content)) ||
			entry.TotalBytes != prefix.TotalBytes+entry.AccountedBytes || entry.TotalStoredBytes != prefix.TotalStoredBytes+uint64(len(entry.Content)) {
			return false
		}
		remaining -= len(entry.Content)
		prefix.Through, prefix.TotalBytes, prefix.TotalStoredBytes, prefix.Digest = entry.Position, entry.TotalBytes, entry.TotalStoredBytes, entry.Digest
	}
	return prefix == p.After
}
