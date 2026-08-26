package conversation

import (
	"context"
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

var (
	// ErrStoreRequired indicates that the conversation usecase has no storage backend.
	ErrStoreRequired = errors.New("internal/usecase/conversation: store required")
	// ErrInvalidRequest indicates that a list request is malformed.
	ErrInvalidRequest = errors.New("internal/usecase/conversation: invalid request")
)

// DirectoryStore pages UID-owned ordinary membership rows.
type DirectoryStore interface {
	ListUserChannelMembershipPage(ctx context.Context, uid string, after metadb.UserChannelMembershipCursor, limit int) ([]metadb.UserChannelMembership, metadb.UserChannelMembershipCursor, bool, error)
}

// HydrationOutcome classifies one aligned channel-head result.
type HydrationOutcome uint8

const (
	HydrationOK HydrationOutcome = iota + 1
	HydrationNoVisibleMessage
	HydrationDelete
	HydrationRetryable
)

// HydrationResult contains bounded Channel-Leader data for one membership.
type HydrationResult struct {
	Key                    ConversationKey
	Outcome                HydrationOutcome
	LastCommittedSeq       uint64
	RetentionThroughSeq    uint64
	CurrentUserLastSendSeq uint64
	LastMessage            *LastMessage
}

// HeadHydrator returns one aligned result per live membership candidate.
type HeadHydrator interface {
	HydrateConversationHeads(ctx context.Context, uid string, memberships []metadb.UserChannelMembership) ([]HydrationResult, error)
}

// MembershipMutationStore reads and mutates UID-owned personal membership
// state. It is the durable source for badge, hide, and activation commands.
type MembershipMutationStore interface {
	GetUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserChannelMembership, bool, error)
	AdvanceUserChannelMembershipReadSeq(ctx context.Context, uid, channelID string, channelType int64, readSeq uint64, updatedAt int64) error
	HideUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64, deletedToSeq uint64, updatedAt int64) error
	ActivateUserChannelMembership(ctx context.Context, uid, channelID string, channelType int64, activatedAt, updatedAt int64) error
}

// Options contains dependencies and read bounds for the conversation usecase.
type Options struct {
	// Directory reads UID-owned membership pages.
	Directory DirectoryStore
	// Hydrator performs bounded channel-head reads for one membership page.
	Hydrator HeadHydrator
	// MembershipMutations owns ordinary per-user badge, hide, and activation state.
	MembershipMutations MembershipMutationStore
	// LegacyMessages reads bounded committed message tails for /conversation/sync compatibility.
	LegacyMessages LegacyMessageReader
	// Now returns the current time for mutation timestamps.
	Now func() time.Time
	// TombstonesRetainedSince reports the oldest deletion coverage still guaranteed.
	// Zero means tombstones have not been expired.
	TombstonesRetainedSince func() int64
}

// App coordinates entry-agnostic conversation list reads.
type App struct {
	directory               DirectoryStore
	hydrator                HeadHydrator
	memberships             MembershipMutationStore
	legacyMessages          LegacyMessageReader
	now                     func() time.Time
	tombstonesRetainedSince func() int64
}

// New creates a conversation usecase.
func New(opts Options) *App {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.TombstonesRetainedSince == nil {
		opts.TombstonesRetainedSince = func() int64 { return 0 }
	}
	return &App{
		directory:               opts.Directory,
		hydrator:                opts.Hydrator,
		memberships:             opts.MembershipMutations,
		legacyMessages:          opts.LegacyMessages,
		now:                     opts.Now,
		tombstonesRetainedSince: opts.TombstonesRetainedSince,
	}
}

// List returns one active-index conversation page for uid.
func (a *App) List(ctx context.Context, req ListRequest) (ListResult, error) {
	if a == nil || a.directory == nil || a.hydrator == nil {
		return ListResult{}, ErrStoreRequired
	}
	return a.listMembershipDirectory(ctx, req)
}

func (a *App) listMembershipDirectory(ctx context.Context, req ListRequest) (ListResult, error) {
	if err := validateListRequest(req); err != nil {
		return ListResult{}, err
	}
	limit := normalizeListLimit(req.Limit)
	rows, next, done, err := a.directory.ListUserChannelMembershipPage(ctx, req.UID, req.Cursor.toMembershipMeta(), limit)
	if err != nil {
		return ListResult{}, err
	}
	result := ListResult{
		ScannedCandidates: len(rows),
		Items:             make([]Conversation, 0, len(rows)),
		Deletes:           make([]ConversationKey, 0),
		Unresolved:        make([]ConversationKey, 0),
		Done:              done,
		HasMore:           !done,
		Coverage:          a.now().UnixNano(),
	}
	result.TombstonesRetainedSince = a.tombstonesRetainedSince()
	result.ResetRequired = req.CompletedCoverage > 0 && result.TombstonesRetainedSince > 0 && req.CompletedCoverage < result.TombstonesRetainedSince
	if !done || len(rows) > 0 {
		result.NextCursor = cursorFromMembershipMeta(next)
	}
	live := make([]metadb.UserChannelMembership, 0, len(rows))
	for _, row := range rows {
		if row.Tombstone {
			result.Deletes = append(result.Deletes, ConversationKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType})
			continue
		}
		live = append(live, row)
	}
	if len(live) == 0 {
		return result, nil
	}
	hydrated, err := a.hydrator.HydrateConversationHeads(ctx, req.UID, live)
	if err != nil {
		return ListResult{}, err
	}
	if len(hydrated) != len(live) {
		return ListResult{}, errors.New("internal/usecase/conversation: misaligned hydration result")
	}
	for i, row := range live {
		head := hydrated[i]
		key := ConversationKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType}
		if head.Key != key {
			return ListResult{}, errors.New("internal/usecase/conversation: misaligned hydration key")
		}
		switch head.Outcome {
		case HydrationDelete:
			result.Deletes = append(result.Deletes, key)
		case HydrationRetryable:
			result.Unresolved = append(result.Unresolved, key)
		case HydrationOK, HydrationNoVisibleMessage:
			if item, ok := conversationFromMembership(row, head); ok {
				result.Items = append(result.Items, item)
			}
		default:
			return ListResult{}, errors.New("internal/usecase/conversation: invalid hydration outcome")
		}
	}
	return result, nil
}

// Retry rebuilds only the requested unresolved conversations from current
// membership and Channel-leader state.
func (a *App) Retry(ctx context.Context, req RetryRequest) (ListResult, error) {
	if a == nil || a.memberships == nil || a.hydrator == nil {
		return ListResult{}, ErrStoreRequired
	}
	if req.UID == "" || len(req.Keys) == 0 || len(req.Keys) > maxListLimit {
		return ListResult{}, ErrInvalidRequest
	}
	result := ListResult{
		ScannedCandidates: len(req.Keys),
		Items:             make([]Conversation, 0, len(req.Keys)),
		Deletes:           make([]ConversationKey, 0),
		Unresolved:        make([]ConversationKey, 0),
		Done:              true,
	}
	live := make([]metadb.UserChannelMembership, 0, len(req.Keys))
	seen := make(map[ConversationKey]struct{}, len(req.Keys))
	for _, key := range req.Keys {
		if key.ChannelID == "" || key.ChannelType <= 0 || key.ChannelType > 255 {
			return ListResult{}, ErrInvalidRequest
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		row, ok, err := a.memberships.GetUserChannelMembership(ctx, req.UID, key.ChannelID, key.ChannelType)
		if err != nil {
			return ListResult{}, err
		}
		if !ok || row.Tombstone {
			result.Deletes = append(result.Deletes, key)
			continue
		}
		live = append(live, row)
	}
	if len(live) == 0 {
		return result, nil
	}
	hydrated, err := a.hydrator.HydrateConversationHeads(ctx, req.UID, live)
	if err != nil {
		return ListResult{}, err
	}
	if len(hydrated) != len(live) {
		return ListResult{}, errors.New("internal/usecase/conversation: misaligned retry hydration result")
	}
	for index, row := range live {
		key := ConversationKey{ChannelID: row.ChannelID, ChannelType: row.ChannelType}
		head := hydrated[index]
		if head.Key != key {
			return ListResult{}, errors.New("internal/usecase/conversation: misaligned retry hydration key")
		}
		switch head.Outcome {
		case HydrationDelete:
			result.Deletes = append(result.Deletes, key)
		case HydrationRetryable:
			result.Unresolved = append(result.Unresolved, key)
		case HydrationOK, HydrationNoVisibleMessage:
			if item, ok := conversationFromMembership(row, head); ok {
				result.Items = append(result.Items, item)
			}
		default:
			return ListResult{}, errors.New("internal/usecase/conversation: invalid retry hydration outcome")
		}
	}
	return result, nil
}

func conversationFromMembership(row metadb.UserChannelMembership, head HydrationResult) (Conversation, bool) {
	visibleMessage := head.LastCommittedSeq >= row.JoinSeq && head.LastCommittedSeq > row.DeletedToSeq
	if !visibleMessage && row.ActivatedAt <= 0 {
		return Conversation{}, false
	}
	visibilityFloor := maxMembershipFloor(joinVisibilityFloor(row.JoinSeq), row.DeletedToSeq, head.RetentionThroughSeq)
	effectiveRead := maxMembershipFloor(visibilityFloor, row.ReadSeq, head.CurrentUserLastSendSeq)
	unread := uint64(0)
	if head.LastCommittedSeq > effectiveRead {
		unread = head.LastCommittedSeq - effectiveRead
	}
	var last *LastMessage
	if visibleMessage && head.LastMessage != nil && head.LastMessage.MessageSeq > visibilityFloor {
		cloned := *head.LastMessage
		cloned.Payload = append([]byte(nil), cloned.Payload...)
		last = &cloned
	}
	return Conversation{
		ChannelID: row.ChannelID, ChannelType: row.ChannelType, JoinSeq: row.JoinSeq,
		ActiveAt: row.ActivatedAt, ReadSeq: row.ReadSeq, DeletedToSeq: row.DeletedToSeq,
		UpdatedAt: row.UpdatedAt, LastMessage: last, Unread: unread,
	}, true
}

func joinVisibilityFloor(joinSeq uint64) uint64 {
	if joinSeq == 0 {
		return 0
	}
	return joinSeq - 1
}

func maxMembershipFloor(values ...uint64) uint64 {
	var out uint64
	for _, value := range values {
		if value > out {
			out = value
		}
	}
	return out
}

func validateListRequest(req ListRequest) error {
	if req.UID == "" {
		return ErrInvalidRequest
	}
	if req.CompletedCoverage < 0 {
		return ErrInvalidRequest
	}
	if req.Limit < 0 || req.Limit > maxListLimit {
		return ErrInvalidRequest
	}
	if req.Cursor != (Cursor{}) && (req.Cursor.ActiveAt < 0 || req.Cursor.ChannelID == "" || req.Cursor.ChannelType == 0) {
		return ErrInvalidRequest
	}
	return nil
}

func (c Cursor) toMembershipMeta() metadb.UserChannelMembershipCursor {
	if c == (Cursor{}) {
		return metadb.UserChannelMembershipCursor{}
	}
	return metadb.UserChannelMembershipCursor{ActivatedAt: c.ActiveAt, ChannelID: c.ChannelID, ChannelType: c.ChannelType}
}

func cursorFromMembershipMeta(cursor metadb.UserChannelMembershipCursor) Cursor {
	return Cursor{ActiveAt: cursor.ActivatedAt, ChannelID: cursor.ChannelID, ChannelType: cursor.ChannelType}
}

func normalizeListLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}
	return limit
}
