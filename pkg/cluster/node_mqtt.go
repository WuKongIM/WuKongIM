package cluster

import (
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

// mqttMetadataStore preserves foreground/maintenance admission without exposing
// the proxy to callers that could retain it across node lifecycle transitions.
func (n *Node) mqttMetadataStore() (*slotproxy.Store, error) {
	if err := n.ensureForeground(); err != nil {
		return nil, err
	}
	if n.defaultSlotProxy == nil {
		return nil, ErrNotStarted
	}
	return n.defaultSlotProxy, nil
}

// ReadMQTT obtains one coherent, authoritative view through a fresh Slot barrier.
func (n *Node) ReadMQTT(ctx context.Context, q metadb.MQTTRead) (metadb.MQTTReadResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTReadResult{}, err
	}
	return s.ReadMQTT(ctx, q)
}

// ReadMQTTRecovery scans one logical hash Slot at its current authority.
func (n *Node) ReadMQTTRecovery(ctx context.Context, hashSlot uint16, q metadb.MQTTRead) (metadb.MQTTReadResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTReadResult{}, err
	}
	return s.ReadMQTTRecovery(ctx, hashSlot, q)
}

// CompareAndSwapMQTTSession persists state; it does not isolate a previous owner.
func (n *Node) CompareAndSwapMQTTSession(ctx context.Context, expected uint64, row metadb.MQTTSession) (metadb.MQTTSessionCASResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTSessionCASResult{}, err
	}
	return s.CompareAndSwapMQTTSession(ctx, expected, row)
}

// ApplyMQTTLifecycle atomically routes a Session/Will lifecycle decision.
func (n *Node) ApplyMQTTLifecycle(ctx context.Context, m metadb.MQTTLifecycleMutation) (metadb.MQTTLifecycleResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTLifecycleResult{}, err
	}
	return s.ApplyMQTTLifecycle(ctx, m)
}

// MutateMQTTSubscription retains the Session owner and revision fence.
func (n *Node) MutateMQTTSubscription(ctx context.Context, m metadb.MQTTSubscriptionMutation) (metadb.MQTTSessionCASResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTSessionCASResult{}, err
	}
	return s.MutateMQTTSubscription(ctx, m)
}

// MutateMQTTDeliveryCursor commits backlog accounting and quota outcomes together.
func (n *Node) MutateMQTTDeliveryCursor(ctx context.Context, m metadb.MQTTDeliveryCursorMutation) (metadb.MQTTDeliveryCursorResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTDeliveryCursorResult{}, err
	}
	return s.MutateMQTTDeliveryCursor(ctx, m)
}

// MutateMQTTWindow commits an outbound exchange or its acknowledgement.
func (n *Node) MutateMQTTWindow(ctx context.Context, m metadb.MQTTWindowMutation) (metadb.MQTTWindowResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTWindowResult{}, err
	}
	return s.MutateMQTTWindow(ctx, m)
}

// CompareAndSwapMQTTSourceBinding routes to source/UID authority, not the Session.
func (n *Node) CompareAndSwapMQTTSourceBinding(ctx context.Context, expected uint64, row metadb.MQTTSourceBinding) (metadb.MQTTSourceBindingResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTSourceBindingResult{}, err
	}
	return s.CompareAndSwapMQTTSourceBinding(ctx, expected, row)
}

// RetireMQTTSourceBinding deletes one proven-ended tombstone on its owner Slot.
func (n *Node) RetireMQTTSourceBinding(ctx context.Context, key metadb.MQTTSourceBindingKey, expected, closedThrough uint64) (metadb.MQTTSourceBindingResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTSourceBindingResult{}, err
	}
	return s.RetireMQTTSourceBinding(ctx, key, expected, closedThrough)
}

// CompareAndSwapMQTTWill preserves obligations beyond the current Session lifetime.
func (n *Node) CompareAndSwapMQTTWill(ctx context.Context, expected uint64, row metadb.MQTTWill) (metadb.MQTTWillResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTWillResult{}, err
	}
	return s.CompareAndSwapMQTTWill(ctx, expected, row)
}

// CompareAndSwapMQTTInboxAdmission preserves foreground admission and Channel routing.
func (n *Node) CompareAndSwapMQTTInboxAdmission(ctx context.Context, expected uint64, row metadb.MQTTInboxAdmission) (metadb.MQTTInboxAdmissionResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTInboxAdmissionResult{}, err
	}
	return s.CompareAndSwapMQTTInboxAdmission(ctx, expected, row)
}

// ReclaimMQTTSession removes ended-lifetime children at their Session authority.
// Its completion receipt proves neither owner isolation nor source retirement.
func (n *Node) ReclaimMQTTSession(ctx context.Context, m metadb.MQTTSessionReclamation) (metadb.MQTTSessionReclamationResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTSessionReclamationResult{}, err
	}
	return s.ReclaimMQTTSession(ctx, m)
}

// BuildMQTTReclamationIndex advances bounded discovery backfill through current
// Slot authority, retaining the same foreground/restore-maintenance admission.
func (n *Node) BuildMQTTReclamationIndex(ctx context.Context, hashSlot uint16) (metadb.MQTTReclamationIndexResult, error) {
	s, err := n.mqttMetadataStore()
	if err != nil {
		return metadb.MQTTReclamationIndexResult{}, err
	}
	return s.BuildMQTTReclamationIndex(ctx, hashSlot)
}
