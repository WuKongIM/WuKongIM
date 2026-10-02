package replication

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

type mqttRetirementAdmissionState struct {
	captured, candidate ch.MQTTReplayAnchorProof
	latest              ch.MQTTReplayRetirementProof
	hasLatest           bool
}

// mqttRetirementAdmissionStore owns bounded independent proof reads after the
// sequencer publishes only its own committed HW. It performs no body cleanup.
type mqttRetirementAdmissionStore interface {
	prepareMQTTReplayRetirement(context.Context, ch.ChannelKey, ch.ChannelID, uint64, ch.MQTTReplayRetirementRequest) (mqttRetirementAdmissionState, error)
}

func (a *storeAdapter) prepareMQTTReplayRetirement(ctx context.Context, key ch.ChannelKey, id ch.ChannelID, hw uint64, q ch.MQTTReplayRetirementRequest) (mqttRetirementAdmissionState, error) {
	var empty mqttRetirementAdmissionState
	if !a.supportsMQTTRetirements() || !a.supportsMQTTAnchors() {
		return empty, ch.ErrInvalidConfig
	}
	st, err := a.cfg.Factory.ChannelStore(key, id)
	if err != nil {
		return empty, err
	}
	if st == nil {
		return empty, ch.ErrInvalidConfig
	}
	defer st.Close()
	anchors, ok := st.(channelstore.MQTTReplayAnchorReader)
	retirements, canRead := st.(channelstore.MQTTReplayLatestRetirementReader)
	if !ok || !canRead {
		return empty, ch.ErrInvalidConfig
	}
	if err = st.StoreCheckpoint(ctx, ch.Checkpoint{HW: hw}); err != nil {
		return empty, err
	}
	var out mqttRetirementAdmissionState
	var found bool
	out.captured, found, err = anchors.LoadMQTTReplayAnchor(ctx, q.Captured.Manifest.LastOffset)
	if err != nil {
		return empty, err
	}
	if !found || out.captured != q.Captured {
		return empty, ch.ErrLogConflict
	}
	out.candidate = out.captured
	if q.Candidate.Manifest.LastOffset != q.Captured.Manifest.LastOffset {
		out.candidate, found, err = anchors.LoadMQTTReplayAnchor(ctx, q.Candidate.Manifest.LastOffset)
		if err != nil {
			return empty, err
		}
		if !found {
			return empty, ch.ErrLogConflict
		}
	}
	if out.candidate != q.Candidate {
		return empty, ch.ErrLogConflict
	}
	out.latest, out.hasLatest, err = retirements.LoadLatestMQTTReplayRetirement(ctx, q.Captured.Prefix().Generation)
	if err != nil {
		return empty, err
	}
	if (!out.hasLatest && out.latest != (ch.MQTTReplayRetirementProof{})) || (out.hasLatest && out.latest.Manifest.LastOffset > hw) {
		return empty, ch.ErrLogConflict
	}
	if err = ctx.Err(); err != nil {
		return empty, err
	}
	return out, nil
}
