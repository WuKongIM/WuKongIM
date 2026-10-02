package message

import (
	"bytes"
	"slices"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// validateMQTTRetirementBackup runs after proposal-chain and anchor validation.
// It requires each decision's independent journal, reference and monotonic prefix.
func validateMQTTRetirementBackup(key ChannelKey, hw uint64, entries []backupRawEntry, proposals map[uint64]durableProposalRecord, identities map[uint64]quorumlog.EntryIdentity) error {
	anchors := make(map[uint64]quorumlog.MQTTReplayAnchor)
	for _, raw := range entries {
		if !bytes.HasPrefix(raw.Key, mqttReplayAnchorPrefix(key)) {
			continue
		}
		position, ok := mqttReplayAnchorPosition(key, raw.Key)
		if !ok {
			return dberrors.ErrCorruptState
		}
		_, a, err := decodeMQTTAnchorJournal(key, position, raw.Value)
		if err != nil {
			return err
		}
		anchors[position] = a
	}
	retirements := make(map[uint64]quorumlog.MQTTReplayRetirement)
	var positions []uint64
	for _, raw := range entries {
		if !bytes.HasPrefix(raw.Key, mqttReplayRetirementPrefix(key)) {
			continue
		}
		position, ok := mqttReplayRetirementPosition(key, raw.Key)
		if !ok || position > hw {
			return dberrors.ErrCorruptState
		}
		row, r, err := decodeMQTTRetirementJournal(key, position, raw.Value)
		if err != nil {
			return err
		}
		proposal, hasProposal := proposals[position]
		entry, hasEntry := identities[position]
		anchor, hasAnchor := anchors[r.AnchorPosition]
		anchorProposal, hasAnchorProposal := proposals[r.AnchorPosition]
		if !hasProposal || !hasEntry || proposal.manifest.Version != quorumlog.MQTTReplayRetirementProposalManifestVersion || !verifyBackupRowIdentity(entry, row) || !hasAnchor || !hasAnchorProposal || anchor != r.Anchor || anchorProposal.manifest.Digest != r.AnchorDigest {
			return dberrors.ErrCorruptState
		}
		if _, duplicate := retirements[position]; duplicate {
			return dberrors.ErrCorruptState
		}
		retirements[position] = r
		positions = append(positions, position)
	}
	for position, p := range proposals {
		if p.manifest.Version == quorumlog.MQTTReplayRetirementProposalManifestVersion {
			if _, found := retirements[position]; !found {
				return dberrors.ErrCorruptState
			}
		}
	}
	slices.Sort(positions)
	var previous quorumlog.MQTTReplayRetirement
	for _, position := range positions {
		next := retirements[position]
		if !validMQTTRetirementAdvance(previous, next) {
			return dberrors.ErrCorruptState
		}
		previous = next
	}
	return nil
}
