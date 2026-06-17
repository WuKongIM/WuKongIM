package management

import (
	"context"
	"errors"
	"strings"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ErrMessageRetentionUnavailable reports that manager retention actions are not wired.
var ErrMessageRetentionUnavailable = errors.New("management: message retention unavailable")

// MessageReader exposes committed channel message reads for manager pages.
type MessageReader interface {
	// QueryMessages returns a manager-facing channel message page.
	QueryMessages(ctx context.Context, req MessageQueryRequest) (MessageQueryPage, error)
}

// MessageRetentionOperator advances channel message retention boundaries.
type MessageRetentionOperator interface {
	// AdvanceMessageRetention evaluates and applies one retention boundary request.
	AdvanceMessageRetention(ctx context.Context, req AdvanceMessageRetentionRequest) (AdvanceMessageRetentionResponse, error)
}

// MessageQueryRequest configures one authoritative channel message page query.
type MessageQueryRequest struct {
	// ChannelID identifies the channel to scan.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// BeforeSeq is the exclusive upper message sequence bound for pagination.
	BeforeSeq uint64
	// Limit is the maximum number of messages to return.
	Limit int
	// MessageID filters the result to matching durable message identifiers when set.
	MessageID uint64
	// ClientMsgNo filters the result to matching client message numbers when set.
	ClientMsgNo string
}

// MessageQueryPage is one authoritative message page in descending order.
type MessageQueryPage struct {
	// Items contains matched channel messages.
	Items []Message
	// HasMore reports whether another matched page exists.
	HasMore bool
	// NextBeforeSeq is the exclusive upper sequence bound for the next page.
	NextBeforeSeq uint64
}

// MessageListCursor identifies the next manager message page position.
type MessageListCursor struct {
	// BeforeSeq is the exclusive upper message sequence bound for the next page.
	BeforeSeq uint64
}

// ListMessagesRequest configures one manager message page request.
type ListMessagesRequest struct {
	// ChannelID identifies the channel to scan.
	ChannelID string
	// ChannelType identifies the logical channel type to scan.
	ChannelType int64
	// Limit is the maximum number of matched messages to return.
	Limit int
	// Cursor is the resume position from the previous page.
	Cursor MessageListCursor
	// MessageID filters the result to matching durable message identifiers when set.
	MessageID uint64
	// ClientMsgNo filters the result to matching client message numbers when set.
	ClientMsgNo string
}

// ListMessagesResponse is one manager message page result.
type ListMessagesResponse struct {
	// Items contains matched messages ordered from newest to oldest.
	Items []Message
	// HasMore reports whether another matched page exists.
	HasMore bool
	// NextCursor identifies the next manager page position when HasMore is true.
	NextCursor MessageListCursor
}

// MessageRetentionStatus is the manager-visible outcome of a retention request.
type MessageRetentionStatus string

const (
	// MessageRetentionStatusAdvanced means metadata was advanced and applied locally.
	MessageRetentionStatusAdvanced MessageRetentionStatus = "advanced"
	// MessageRetentionStatusWouldAdvance means a dry-run found a safe advance.
	MessageRetentionStatusWouldAdvance MessageRetentionStatus = "would_advance"
	// MessageRetentionStatusNoop means the requested boundary is already retained.
	MessageRetentionStatusNoop MessageRetentionStatus = "noop"
	// MessageRetentionStatusBlocked means safety gates currently prevent an advance.
	MessageRetentionStatusBlocked MessageRetentionStatus = "blocked"
)

// MessageRetentionBlockedReason explains which safety gate blocked an advance.
type MessageRetentionBlockedReason string

const (
	// MessageRetentionBlockedReasonNone means no safety gate blocked the request.
	MessageRetentionBlockedReasonNone MessageRetentionBlockedReason = ""
	// MessageRetentionBlockedReasonReplayCursor means committed replay has not durably reached the boundary.
	MessageRetentionBlockedReasonReplayCursor MessageRetentionBlockedReason = "replay_cursor"
	// MessageRetentionBlockedReasonMinISRMatchOffset means at least one ISR member has not reached the boundary.
	MessageRetentionBlockedReasonMinISRMatchOffset MessageRetentionBlockedReason = "min_isr_match_offset"
	// MessageRetentionBlockedReasonHW means the committed high watermark is below the requested boundary.
	MessageRetentionBlockedReasonHW MessageRetentionBlockedReason = "hw"
	// MessageRetentionBlockedReasonCheckpointHW means the durable checkpoint is below the requested boundary.
	MessageRetentionBlockedReasonCheckpointHW MessageRetentionBlockedReason = "checkpoint_hw"
	// MessageRetentionBlockedReasonCurrentBoundary means the request does not exceed the current boundary.
	MessageRetentionBlockedReasonCurrentBoundary MessageRetentionBlockedReason = "current_boundary"
)

// AdvanceMessageRetentionRequest configures one manager retention-boundary advance.
type AdvanceMessageRetentionRequest struct {
	// ChannelID identifies the channel whose history prefix should be retained.
	ChannelID string
	// ChannelType identifies the channel namespace for ChannelID.
	ChannelType int64
	// ThroughSeq is the requested highest unavailable message sequence.
	ThroughSeq uint64
	// DryRun reports the calculated outcome without mutating metadata or runtime state.
	DryRun bool
}

// AdvanceMessageRetentionResponse reports one retention-boundary advance outcome.
type AdvanceMessageRetentionResponse struct {
	// ChannelID identifies the channel whose history prefix was evaluated.
	ChannelID string
	// ChannelType identifies the channel namespace for ChannelID.
	ChannelType int64
	// RequestedThroughSeq is the operator-requested highest unavailable sequence.
	RequestedThroughSeq uint64
	// AdvancedThroughSeq is the safe boundary that was or would be advanced.
	AdvancedThroughSeq uint64
	// MinAvailableSeq is the first sequence visible after the resulting boundary.
	MinAvailableSeq uint64
	// Status is the manager-visible retention request outcome.
	Status MessageRetentionStatus
	// BlockedReason explains why status is blocked.
	BlockedReason MessageRetentionBlockedReason
}

// ListMessages returns one authoritative channel message page.
func (a *App) ListMessages(ctx context.Context, req ListMessagesRequest) (ListMessagesResponse, error) {
	channelID := strings.TrimSpace(req.ChannelID)
	if channelID == "" || req.ChannelType <= 0 || req.ChannelType > 255 || req.Limit <= 0 {
		return ListMessagesResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.messages == nil {
		return ListMessagesResponse{}, nil
	}

	page, err := a.messages.QueryMessages(ctx, MessageQueryRequest{
		ChannelID:   channelID,
		ChannelType: req.ChannelType,
		BeforeSeq:   req.Cursor.BeforeSeq,
		Limit:       req.Limit,
		MessageID:   req.MessageID,
		ClientMsgNo: req.ClientMsgNo,
	})
	if err != nil {
		return ListMessagesResponse{}, err
	}
	resp := ListMessagesResponse{
		Items:   cloneMessages(page.Items),
		HasMore: page.HasMore,
	}
	if page.HasMore {
		resp.NextCursor = MessageListCursor{BeforeSeq: page.NextBeforeSeq}
	}
	return resp, nil
}

// AdvanceMessageRetention advances one channel's history retention boundary.
func (a *App) AdvanceMessageRetention(ctx context.Context, req AdvanceMessageRetentionRequest) (AdvanceMessageRetentionResponse, error) {
	req.ChannelID = strings.TrimSpace(req.ChannelID)
	if req.ChannelID == "" || req.ChannelType <= 0 || req.ChannelType > 255 || req.ThroughSeq == 0 {
		return AdvanceMessageRetentionResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.messageRetention == nil {
		return AdvanceMessageRetentionResponse{}, ErrMessageRetentionUnavailable
	}
	return a.messageRetention.AdvanceMessageRetention(ctx, req)
}

func cloneMessages(items []Message) []Message {
	out := make([]Message, 0, len(items))
	for _, item := range items {
		item.Payload = append([]byte(nil), item.Payload...)
		out = append(out, item)
	}
	return out
}
