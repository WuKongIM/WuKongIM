package channel

import "context"

// MQTTReplayOriginalRequest binds a bounded page to the protected consumer
// boundary and its complete accounted frontier, even when reading a small page.
type MQTTReplayOriginalRequest struct {
	Request MQTTReplayRequest
	// StartAfter is the consumer's fixed boundary, never a metadata cache hint.
	StartAfter uint64
	// AccountedThrough must be covered by the captured committed anchor.
	AccountedThrough uint64
}

func (q MQTTReplayOriginalRequest) PlanRequest() MQTTReplayPlanRequest {
	return MQTTReplayPlanRequest{ChannelID: q.Request.ChannelID, ExpectedChannelEpoch: q.Request.ExpectedChannelEpoch, ExpectedLeaderEpoch: q.Request.ExpectedLeaderEpoch, ExpectedRouteGeneration: q.Request.ExpectedRouteGeneration, Generation: q.Request.Range.Generation}
}
func (q MQTTReplayOriginalRequest) Valid() bool {
	return q.PlanRequest().Valid() && q.Request.Range.Valid() && q.StartAfter < q.Request.Range.From && q.Request.Range.Through <= q.AccountedThrough
}

// MQTTReplayOriginalResult owns one page and the exact plan used to select its
// anchor. It grants no consumer mutation, permission or source-release authority.
type MQTTReplayOriginalResult struct {
	Plan MQTTReplayPlan
	Page MQTTReplayConsumerPage
}

// ValidFor verifies source coverage and the page/anchor association together.
func (p MQTTReplayOriginalResult) ValidFor(q MQTTReplayOriginalRequest) bool {
	return q.Valid() && p.Plan.ValidFor(q.PlanRequest()) && p.Plan.Source.StartAfter <= q.StartAfter && p.Plan.Source.CommittedThrough >= q.AccountedThrough && p.Plan.HasAnchor && p.Plan.Anchor.Anchor.Through >= q.AccountedThrough && p.Page.ValidFor(q.Request.ChannelID, q.Request.Range) && p.Page.Before.StartAfter == p.Plan.Source.StartAfter && (p.Page.After.Through != p.Plan.Anchor.Anchor.Through || p.Page.After == p.Plan.Anchor.Prefix())
}

// MQTTReplayOriginalReader combines read-only planning and anchored content
// under one fresh serving authority. Implementations retain bounded admission.
type MQTTReplayOriginalReader interface {
	ReadMQTTOriginals(context.Context, MQTTReplayOriginalRequest) (MQTTReplayOriginalResult, error)
}
