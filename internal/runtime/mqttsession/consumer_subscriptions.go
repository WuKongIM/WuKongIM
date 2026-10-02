package mqttsession

import (
	"cmp"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// consumerSubscriptionCandidates validates the entire pending page before any
// dispatch. Per-row continuations survive pressure on terminal ScanIndex pages.
func consumerSubscriptionCandidates(q meta.MQTTRead, r meta.MQTTReadResult) ([]meta.MQTTReadCursor, error) {
	if q.Kind != meta.MQTTReadSubscriptionRecovery || meta.ValidateMQTTRead(q) != nil || r.Session != nil || r.Runtime != nil || r.Admission != nil || r.Membership != nil || r.Accounting != nil || len(r.Directory)+len(r.SourceOwners)+len(r.Sessions)+len(r.Bindings)+len(r.DeliveryCursors)+len(r.Inflight)+len(r.Wills) != 0 || len(r.Subscriptions) > q.Limit || len(r.Subscriptions) == 0 && !r.Done {
		return nil, ErrDeadlineScanEvidence
	}
	check := q
	check.After = r.After
	if meta.ValidateMQTTRead(check) != nil {
		return nil, ErrDeadlineScanEvidence
	}
	after := q.After
	out := make([]meta.MQTTReadCursor, 0, len(r.Subscriptions))
	for _, s := range r.Subscriptions {
		if meta.ValidateMQTTSubscription(s) != nil || (s.Stage != meta.MQTTSubscriptionPreparing && s.Stage != meta.MQTTSubscriptionRemoving) {
			return nil, ErrDeadlineScanEvidence
		}
		next := meta.MQTTReadCursor{Subscription: meta.MQTTSubscriptionRecoveryCursor{RecoveryAtMS: s.RecoveryAtMS, Namespace: s.Namespace, ClientID: s.ClientID, SessionGeneration: s.SessionGeneration, Topic: s.Topic}}
		if compareSubscriptionRecovery(after.Subscription, next.Subscription) >= 0 {
			return nil, ErrDeadlineScanEvidence
		}
		out = append(out, next)
		after = next
	}
	if r.After != after && (!r.Done || r.After != q.After) {
		return nil, ErrDeadlineScanEvidence
	}
	return out, nil
}

func compareSubscriptionRecovery(a, b meta.MQTTSubscriptionRecoveryCursor) int {
	if c := cmp.Compare(a.RecoveryAtMS, b.RecoveryAtMS); c != 0 {
		return c
	}
	for _, p := range [][2]string{{a.Namespace, b.Namespace}, {a.ClientID, b.ClientID}} {
		if c := cmp.Compare(len(p[0]), len(p[1])); c != 0 {
			return c
		}
		if c := strings.Compare(p[0], p[1]); c != 0 {
			return c
		}
	}
	if c := cmp.Compare(a.SessionGeneration, b.SessionGeneration); c != 0 {
		return c
	}
	if c := cmp.Compare(len(a.Topic), len(b.Topic)); c != 0 {
		return c
	}
	return strings.Compare(a.Topic, b.Topic)
}
