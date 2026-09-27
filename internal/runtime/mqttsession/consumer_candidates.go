package mqttsession

import (
	"cmp"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// consumerCandidates validates complete bounded recovery pages before effects.
func consumerCandidates(q meta.MQTTRead, r meta.MQTTReadResult) ([]meta.MQTTReadCursor, error) {
	if q.Kind != meta.MQTTReadSourceRecovery || meta.ValidateMQTTRead(q) != nil || r.Session != nil || r.Runtime != nil || r.Admission != nil || r.Membership != nil || r.Accounting != nil || len(r.Directory)+len(r.SourceOwners)+len(r.Sessions)+len(r.Subscriptions)+len(r.DeliveryCursors)+len(r.Inflight)+len(r.Wills) != 0 || len(r.Bindings) > q.Limit || len(r.Bindings) == 0 && !r.Done {
		return nil, ErrDeadlineScanEvidence
	}
	check := q
	check.After = r.After
	if meta.ValidateMQTTRead(check) != nil {
		return nil, ErrDeadlineScanEvidence
	}
	after := q.After
	out := make([]meta.MQTTReadCursor, 0, len(r.Bindings))
	for _, b := range r.Bindings {
		if meta.ValidateMQTTSourceBinding(b) != nil || b.Stage == meta.MQTTBindingRemoved {
			return nil, ErrDeadlineScanEvidence
		}
		next := meta.MQTTReadCursor{SourceRecovery: meta.MQTTSourceBindingRecoveryCursor{RecoveryAtMS: b.RecoveryAtMS, Key: b.Key}}
		if compareConsumerCursor(after.SourceRecovery, next.SourceRecovery) >= 0 {
			return nil, ErrDeadlineScanEvidence
		}
		out = append(out, next)
		after = next
	}
	// Terminal ScanIndex pages may retain the request cursor. Row witnesses
	// above still supply exact continuations when cohort pressure splits a page.
	if r.After != after && (!r.Done || r.After != q.After) {
		return nil, ErrDeadlineScanEvidence
	}
	return out, nil
}

// Index order uses length-prefixed strings and all identity tie breakers.
func compareConsumerCursor(a, b meta.MQTTSourceBindingRecoveryCursor) int {
	if c := cmp.Compare(a.RecoveryAtMS, b.RecoveryAtMS); c != 0 {
		return c
	}
	if c := meta.CompareMQTTBindingOwners(a.Key.Owner, b.Key.Owner); c != 0 {
		return c
	}
	for _, p := range [][2]string{{a.Key.Namespace, b.Key.Namespace}, {a.Key.ClientID, b.Key.ClientID}} {
		if c := cmp.Compare(len(p[0]), len(p[1])); c != 0 {
			return c
		}
		if c := strings.Compare(p[0], p[1]); c != 0 {
			return c
		}
	}
	if c := cmp.Compare(a.Key.SessionGeneration, b.Key.SessionGeneration); c != 0 {
		return c
	}
	return cmp.Compare(a.Key.SubscriptionGeneration, b.Key.SubscriptionGeneration)
}
