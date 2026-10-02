package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// MQTTReplayMessage owns one strictly decoded original message and its immutable
// shared-content reference. Internal requires an independently committed native
// control format; payload text and message flags never supply that proof.
type MQTTReplayMessage struct {
	Message                                                      channel.Message
	Internal                                                     bool
	ContentVersion, AccountedBytes, TotalBytes, TotalStoredBytes uint64
	ContentHash, Digest                                          [32]byte
}

// MQTTReplayMessages retains contiguous source coverage, including controls.
// Consumer qualification, permissions and progress remain caller responsibilities.
type MQTTReplayMessages struct {
	Before, After MQTTReplayState
	Records       []MQTTReplayMessage
}

// ReadMQTTReplayMessages shares one snapshot across anchor validation, immutable
// content decoding and committed native identity checks. No original rows or
// permissive compatibility fallback are used, even after physical history trim.
func (s *ChannelStore) ReadMQTTReplayMessages(ctx context.Context, generation string, anchor, from, through uint64, opts ReadOptions) (MQTTReplayMessages, error) {
	var empty MQTTReplayMessages
	if err := validateMQTTReplayRead(generation, from, through, opts); err != nil {
		return empty, toChannelError(err)
	}
	if anchor == 0 || through >= anchor {
		return empty, toChannelError(dberrors.ErrInvalidArgument)
	}
	if err := s.beginUse(); err != nil {
		return empty, err
	}
	defer s.endUse()
	l := s.log
	if err := l.beginUse(); err != nil {
		return empty, toChannelError(err)
	}
	defer l.endUse()
	if err := ctxErr(ctx); err != nil {
		return empty, err
	}
	view, err := l.db.engine.NewSnapshot()
	if err != nil {
		return empty, toChannelError(err)
	}
	defer view.Close()
	raw, err := readMQTTReplayAnchorFrom(ctx, view, l.key, generation, anchor, from, through, opts)
	if err != nil {
		return empty, toChannelError(err)
	}
	out := MQTTReplayMessages{Before: raw.Before, After: raw.After, Records: make([]MQTTReplayMessage, 0, len(raw.Records))}
	for _, r := range raw.Records {
		if err := ctxErr(ctx); err != nil {
			return empty, err
		}
		row, err := validateMQTTReplayTransferRecord(l.key, generation, r)
		if err != nil {
			return empty, toChannelError(err)
		}
		entry, manifest, err := mqttReplayCommittedEntry(view, l.key, r.Position, anchor)
		if err != nil {
			return empty, toChannelError(err)
		}
		if row.ChannelID != l.id.ID || row.ChannelType != l.id.Type || !verifyBackupRowIdentity(entry, row) {
			return empty, toChannelError(dberrors.ErrCorruptState)
		}
		internal := false
		switch manifest.Version {
		case quorumlog.ProposalManifestVersion, quorumlog.ExpirationProposalManifestVersion, quorumlog.PublicationProposalManifestVersion:
		case quorumlog.MQTTSourceProposalManifestVersion, quorumlog.MQTTReplayAnchorProposalManifestVersion, quorumlog.MQTTReplayRetirementProposalManifestVersion, quorumlog.RecoveryBarrierProposalManifestVersion:
			internal = true
		default:
			return empty, toChannelError(dberrors.ErrCorruptState)
		}
		out.Records = append(out.Records, MQTTReplayMessage{Message: channelMessageFromOwnedRow(row), Internal: internal,
			ContentVersion: r.ContentVersion, AccountedBytes: r.AccountedBytes, TotalBytes: r.TotalBytes, TotalStoredBytes: r.TotalStoredBytes, ContentHash: r.ContentHash, Digest: r.Digest})
	}
	if err := ctxErr(ctx); err != nil {
		return empty, err
	}
	return out, nil
}
