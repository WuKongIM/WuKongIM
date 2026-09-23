package message

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

const mqttMaintenanceTailLimit = 64

// mqttMaintenanceOnly proves a complete bounded suffix from native identities
// and committed control journals in the caller's pinned view. Longer tails or
// other native records remain copyable; payloads and SyncOnce alone prove nothing.
func mqttMaintenanceOnly(ctx context.Context, view proposalReadView, key ChannelKey, latest MQTTReplayAnchorProof, through uint64) (bool, error) {
	if through <= latest.Anchor.Through || through-latest.Anchor.Through > mqttMaintenanceTailLimit {
		return false, nil
	}
	for position := latest.Anchor.Through + 1; ; position++ {
		if err := ctxErr(ctx); err != nil {
			return false, err
		}
		_, manifest, err := mqttReplayCommittedEntry(view, key, position, through)
		if err != nil {
			return false, err
		}
		switch manifest.Version {
		case quorumlog.MQTTReplayAnchorProposalManifestVersion:
			p, found, err := loadMQTTReplayAnchorFrom(view, key, position)
			if err != nil {
				return false, err
			}
			if !found || p.Manifest != manifest || p.Anchor.SourceCommand != latest.Anchor.SourceCommand || p.Anchor.StartAfter != latest.Anchor.StartAfter {
				return false, dberrors.ErrCorruptState
			}
		case quorumlog.MQTTReplayRetirementProposalManifestVersion:
			p, found, err := loadMQTTReplayRetirementFrom(view, key, position)
			if err != nil {
				return false, err
			}
			if !found || p.Manifest != manifest || p.Retirement.Anchor.SourceCommand != latest.Anchor.SourceCommand || p.Retirement.Anchor.StartAfter != latest.Anchor.StartAfter {
				return false, dberrors.ErrCorruptState
			}
		default:
			return false, nil
		}
		if position == through {
			return true, nil
		}
	}
}
