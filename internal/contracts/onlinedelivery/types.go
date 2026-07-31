// Package onlinedelivery defines the canonical contracts crossing the Online Delivery seam.
package onlinedelivery

import (
	"github.com/WuKongIM/WuKongIM/internal/contracts/authority"
	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
)

// Mode identifies whether a Recipient Delivery Plan follows a durable commit
// or represents transient NoPersist delivery.
type Mode uint8

const (
	// ModeDurable identifies Online Delivery after a quorum-backed message commit.
	ModeDurable Mode = iota + 1
	// ModeTransient identifies NoPersist Online Delivery.
	ModeTransient
)

// Valid reports whether mode is a supported Online Delivery mode.
func (m Mode) Valid() bool {
	return m == ModeDurable || m == ModeTransient
}

// RecipientTargetBatch groups recipients that share one exact authority target.
type RecipientTargetBatch struct {
	// Target is the exact recipient authority fence resolved before admission.
	Target authority.Target
	// Recipients are the recipients owned by Target.
	Recipients []channelappendcontract.Recipient
}

// Clone returns an independent recipient target batch.
func (b RecipientTargetBatch) Clone() RecipientTargetBatch {
	b.Recipients = append([]channelappendcontract.Recipient(nil), b.Recipients...)
	return b
}

// RecipientDeliveryPlan carries one message and a bounded set of exact-target
// recipient groups into Online Delivery.
type RecipientDeliveryPlan struct {
	// Mode explicitly distinguishes durable and transient Online Delivery.
	Mode Mode
	// Event is the immutable message being delivered.
	Event channelappendcontract.CommittedEnvelope
	// Targets preserve the exact authority fences resolved for the recipients.
	Targets []RecipientTargetBatch
}

// Clone returns an independent delivery plan for adapters that retain input.
func (p RecipientDeliveryPlan) Clone() RecipientDeliveryPlan {
	p.Event = p.Event.Clone()
	p.Targets = append([]RecipientTargetBatch(nil), p.Targets...)
	for i := range p.Targets {
		p.Targets[i] = p.Targets[i].Clone()
	}
	return p
}

// RecipientCount returns the number of recipient entries carried by the plan.
func (p RecipientDeliveryPlan) RecipientCount() int {
	total := 0
	for _, target := range p.Targets {
		total += len(target.Recipients)
	}
	return total
}

// Route describes one exact online recipient endpoint.
type Route struct {
	// UID is the recipient user ID for this endpoint.
	UID string
	// OwnerNodeID is the node that owns the recipient gateway session.
	OwnerNodeID uint64
	// OwnerBootID fences stale owner-node process incarnations.
	OwnerBootID uint64
	// OwnerSeq fences stale owner-session authority observations.
	OwnerSeq uint64
	// SessionID is the recipient owner-local gateway session identifier.
	SessionID uint64
	// DeviceID identifies the recipient client device.
	DeviceID string
	// DeviceFlag carries protocol device category metadata.
	DeviceFlag uint8
	// DeviceLevel carries protocol device priority metadata.
	DeviceLevel uint8
}

// OwnerPush groups exact recipient routes owned by one node.
type OwnerPush struct {
	// OwnerNodeID is the recipient owner node that should accept the push.
	OwnerNodeID uint64
	// Event is the immutable message being pushed.
	Event channelappendcontract.CommittedEnvelope
	// Routes are the recipient endpoints owned by OwnerNodeID.
	Routes []Route
}

// Clone returns an independent owner push for serialization or retention.
func (p OwnerPush) Clone() OwnerPush {
	p.Event = p.Event.Clone()
	p.Routes = append([]Route(nil), p.Routes...)
	return p
}

// OwnerPushResult reports how an owner node classified pushed routes.
type OwnerPushResult struct {
	// Accepted routes were accepted for delivery by the owner node.
	Accepted []Route
	// Retryable routes should be retried by Online Delivery.
	Retryable []Route
	// Dropped routes should not be retried.
	Dropped []Route
}

// Clone returns an independent owner push result.
func (r OwnerPushResult) Clone() OwnerPushResult {
	r.Accepted = append([]Route(nil), r.Accepted...)
	r.Retryable = append([]Route(nil), r.Retryable...)
	r.Dropped = append([]Route(nil), r.Dropped...)
	return r
}
