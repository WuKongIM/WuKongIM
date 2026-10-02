package mqttsession

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// qualification projects only monotonic lifetime termination. An offline parent,
// absent cursor or unfinished unsubscribe never proves inbox qualification ended.
// UID qualification does not carry Channel protection or consumer completion.
func (p *SourceProgress) qualification(ctx context.Context, b meta.MQTTSourceBinding) (SourceProgressResult, error) {
	out := SourceProgressResult{Binding: b, NeedsRemoval: b.Stage == meta.MQTTBindingRemoving}
	r, err := p.read(ctx, meta.MQTTRead{Kind: meta.MQTTReadSession, Namespace: b.Key.Namespace, ClientID: b.Key.ClientID})
	if err != nil {
		return SourceProgressResult{}, err
	}
	if len(r.Bindings) != 0 || len(r.DeliveryCursors) != 0 || !removalSessionMatches(r.Session, b) {
		return SourceProgressResult{}, ErrEvidence
	}
	if r.Session.Generation == b.Key.SessionGeneration && r.Session.State != meta.MQTTSessionEnded {
		if b.ReleaseReason == meta.MQTTBindingSessionEnded {
			return SourceProgressResult{}, ErrEvidence
		}
		return out, nil
	}
	if b.ReleaseReason == meta.MQTTBindingSessionEnded {
		return out, nil
	}
	if r.Session.Revision <= b.ProgressRevision {
		return SourceProgressResult{}, ErrEvidence
	}
	next := b
	next.Stage, next.ReleaseReason = meta.MQTTBindingRemoving, meta.MQTTBindingSessionEnded
	next.ProgressRevision = r.Session.Revision
	return p.write(ctx, b, next)
}
