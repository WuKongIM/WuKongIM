package meta

import "github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"

func validateMQTTInboxDrain(r MQTTSourceBinding) error {
	if r.DrainVersion == 0 {
		if r.DrainAfterSourceID != "" || r.DrainAfterSourceGeneration != "" || r.DrainDone {
			return dberrors.ErrInvalidArgument
		}
		return nil
	}
	if r.DrainVersion != 1 || r.Key.Owner.Kind != MQTTBindingUID || r.Stage < MQTTBindingRemoving || r.Stage == MQTTBindingRemoved && r.ReleaseReason == MQTTBindingDrained && !r.DrainDone {
		return dberrors.ErrInvalidArgument
	}
	if r.DrainAfterSourceID == "" {
		if r.DrainAfterSourceGeneration != "" {
			return dberrors.ErrInvalidArgument
		}
	} else {
		k := r.Key
		if validateMQTTDeliveryCursorKey(MQTTDeliveryCursorKey{Namespace: k.Namespace, ClientID: k.ClientID, SessionGeneration: k.SessionGeneration, SubscriptionGeneration: k.SubscriptionGeneration, SourceKind: MQTTSourceChannel, SourceID: r.DrainAfterSourceID, SourceGeneration: r.DrainAfterSourceGeneration}) != nil {
			return dberrors.ErrInvalidArgument
		}
	}
	if (r.DrainAfterSourceID != "" || r.DrainDone) && r.ProgressRevision < r.IntentRevision {
		return dberrors.ErrInvalidArgument
	}
	return nil
}

// A marker is initialized empty before scanning and never regresses. Lifetime
// termination can supersede unfinished draining but cannot erase its evidence.
func validMQTTInboxDrainTransition(old, next MQTTSourceBinding) bool {
	if next.Stage == MQTTBindingRemoved && next.ReleaseReason == MQTTBindingDrained && (next.DrainVersion != 1 || !next.DrainDone) {
		return false
	}
	if next.DrainVersion == 0 {
		return old.DrainVersion == 0
	}
	if next.DiscoveryAfterChannelID != old.DiscoveryAfterChannelID || next.DiscoveryAfterChannelType != old.DiscoveryAfterChannelType || next.DiscoveryDone != old.DiscoveryDone {
		return false
	}
	if old.DrainVersion == 0 {
		return next.DrainAfterSourceID == "" && next.DrainAfterSourceGeneration == "" && !next.DrainDone
	}
	if old.DrainDone {
		return next.DrainDone && next.DrainAfterSourceID == old.DrainAfterSourceID && next.DrainAfterSourceGeneration == old.DrainAfterSourceGeneration
	}
	for _, pair := range [][2]string{{old.DrainAfterSourceID, next.DrainAfterSourceID}, {old.DrainAfterSourceGeneration, next.DrainAfterSourceGeneration}} {
		if len(pair[0]) != len(pair[1]) {
			return len(pair[0]) < len(pair[1])
		}
		if pair[0] != pair[1] {
			return pair[0] < pair[1]
		}
	}
	return true
}
