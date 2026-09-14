package message

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/google/uuid"
)

// UpdateStore persists latest replacements independently from immutable logs.
type UpdateStore interface {
	ApplyMessageUpdate(context.Context, metadb.MessageUpdateMutation) (metadb.MessageUpdateMutationResult, error)
	ReadMessageUpdatesBatch(context.Context, []metadb.MessageUpdateRead) ([]metadb.MessageUpdatePage, error)
	GetChannelRuntimeMeta(context.Context, string, int64) (metadb.ChannelRuntimeMeta, error)
}

// UpdateError is a stable business rejection, distinct from uncertain I/O.
type UpdateError string

func (e UpdateError) Error() string { return string(e) }

const (
	ErrUpdateInvalid     UpdateError = "invalid_request"
	ErrUpdateMissing     UpdateError = "message_not_found"
	ErrNotUpdatable      UpdateError = "message_not_updatable"
	ErrUpdateUnavailable UpdateError = "message_update_unavailable"
)

// UpdateMessageCommand replaces the complete opaque payload. Backend callers
// own editing authorization and must pass the version actually shown to users.
type UpdateMessageCommand struct {
	LoginUID        string
	ChannelID       string
	ChannelType     uint8
	MessageID       uint64
	ExpectedVersion uint64
	// ExpectedContentEpoch fences edits created before a successful restore.
	ExpectedContentEpoch uint64
	RequestID            string
	Payload              []byte
}

// UpdateMessageResult preserves identity and reports the originally committed
// result on an idempotent retry, even when a later edit has already succeeded.
type UpdateMessageResult struct {
	MessageID   uint64
	MessageSeq  uint64
	Version     uint64
	UpdatedAtMS int64
}

func (a *App) updateHead(ctx context.Context, id ChannelID) (metadb.MessageUpdateHead, error) {
	pages, err := a.updates.ReadMessageUpdatesBatch(ctx, []metadb.MessageUpdateRead{{ChannelID: id.ID, ChannelType: int64(id.Type)}})
	if err != nil {
		return metadb.MessageUpdateHead{}, err
	}
	if len(pages) != 1 {
		return metadb.MessageUpdateHead{}, ErrUpdateUnavailable
	}
	if pages[0].Head.Generation != "" {
		return pages[0].Head, nil
	}
	generation, err := uuid.NewRandom()
	if err != nil {
		return metadb.MessageUpdateHead{}, err
	}
	result, err := a.updates.ApplyMessageUpdate(ctx, metadb.MessageUpdateMutation{Op: "init", ChannelID: id.ID, ChannelType: int64(id.Type), Generation: generation.String()})
	if err != nil {
		return metadb.MessageUpdateHead{}, err
	}
	if result.Status != "ok" {
		return metadb.MessageUpdateHead{}, UpdateError(result.Status)
	}
	return result.Head, nil
}

// UpdateMessage validates the immutable original, then submits a lifecycle-
// fenced CAS. Updating contents cannot change permissions or message order.
func (a *App) UpdateMessage(ctx context.Context, q UpdateMessageCommand) (UpdateMessageResult, error) {
	var out UpdateMessageResult
	if a == nil || a.updates == nil || a.lookupReader == nil {
		return out, ErrUpdateUnavailable
	}
	q.ChannelID = strings.TrimSpace(q.ChannelID)
	q.RequestID = strings.TrimSpace(q.RequestID)
	if q.ChannelID == "" || q.ChannelType == 0 || q.MessageID == 0 || q.RequestID == "" || len(q.RequestID) > 128 || len(q.Payload) == 0 || len(q.Payload) > metadb.MaxMessageUpdatePayload {
		return out, ErrUpdateInvalid
	}
	if a.commandChannels.IsCommandChannel(strings.TrimSpace(q.ChannelID)) {
		return out, ErrNotUpdatable
	}
	if q.ChannelType == 1 {
		var err error
		q.ChannelID, err = runtimechannelid.NormalizePersonChannel(strings.TrimSpace(q.LoginUID), q.ChannelID)
		if err != nil {
			return out, ErrUpdateInvalid
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	epoch := uint64(0)
	if a.contentEpoch != nil {
		var err error
		epoch, err = a.contentEpoch(ctx)
		if err != nil {
			return out, err
		}
	}
	if q.ExpectedContentEpoch != epoch {
		return out, UpdateError("content_epoch_conflict")
	}
	id := ChannelID{ID: q.ChannelID, Type: q.ChannelType}
	head, err := a.updateHead(ctx, id)
	if err != nil {
		return out, err
	}
	runtime, err := a.updates.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
	if err != nil {
		return out, err
	}
	reads, err := a.lookupReader.ReadCommittedMessages(ctx, []MessageScanQuery{{ChannelID: id, MessageID: q.MessageID, Limit: 1, MaxBytes: metadb.MaxMessageUpdatePayload}})
	if err != nil {
		return out, err
	}
	if len(reads) != 1 {
		return out, ErrUpdateUnavailable
	}
	if reads[0].Err != nil {
		return out, reads[0].Err
	}
	if len(reads[0].Messages) != 1 {
		return out, ErrUpdateMissing
	}
	original := reads[0].Messages[0]
	if original.MessageID != q.MessageID || original.MessageSeq == 0 || original.ChannelID != id.ID || original.ChannelType != id.Type {
		return out, ErrUpdateUnavailable
	}
	if original.Flags.SyncOnce || original.Flags.NoPersist || isLegacyStreamMessage(original.Setting) {
		return out, ErrNotUpdatable
	}
	// Exclude generated timestamps and routing guards from the request digest.
	digestInput, _ := json.Marshal(struct {
		Version uint64
		Payload []byte
	}{q.ExpectedVersion, q.Payload})
	digest := sha256.Sum256(digestInput)
	applied, err := a.updates.ApplyMessageUpdate(ctx, metadb.MessageUpdateMutation{Op: "update", ChannelID: id.ID, ChannelType: int64(id.Type), Generation: head.Generation, ReplicaSet: head.ReplicaSet, MessageID: q.MessageID, MessageSeq: original.MessageSeq, ExpectedVersion: q.ExpectedVersion, ExpectedContentEpoch: q.ExpectedContentEpoch, ExpectedChannelEpoch: runtime.ChannelEpoch, ExpectedRouteGeneration: runtime.RouteGeneration, RequestID: q.RequestID, Digest: hex.EncodeToString(digest[:]), Payload: q.Payload, UpdatedAtMS: a.now().UnixMilli()})
	if err != nil {
		return out, err
	}
	if applied.Status != "ok" {
		return out, UpdateError(applied.Status)
	}
	if a.updateCommitted != nil {
		a.updateCommitted(metadb.MessageUpdate{ChannelID: id.ID, ChannelType: int64(id.Type),
			MessageID: applied.Request.MessageID, MessageSeq: applied.Request.MessageSeq, Version: applied.Request.Version})
	}
	return UpdateMessageResult{MessageID: applied.Request.MessageID, MessageSeq: applied.Request.MessageSeq, Version: applied.Request.Version, UpdatedAtMS: applied.Request.UpdatedAtMS}, nil
}

// MessageUpdatesQuery synchronizes only one caller-visible channel.
type MessageUpdatesQuery struct {
	LoginUID     string
	ChannelID    string
	ChannelType  uint8
	UpdateCursor string
	Limit        int
}

// MessageUpdatesResult distinguishes bootstrap/reset from a normal empty page.
type MessageUpdatesResult struct {
	Updates          []SyncedMessage
	NextUpdateCursor string
	More             bool
	ResetRequired    bool
}

type updateCursor struct {
	RestoreEpoch uint64
	Format       int
	// Binding hashes the canonical caller/channel tuple so supported long
	// identities cannot make a server-issued cursor exceed its own input bound.
	Binding string `json:",omitempty"`
	// Raw identity fields are accepted only for format-1 migration to a baseline.
	UID           string `json:",omitempty"`
	ChannelID     string `json:",omitempty"`
	ChannelType   uint8  `json:",omitempty"`
	Generation    string
	SourceVersion uint64
	JoinSeq       uint64
	DeletedToSeq  uint64
	After         uint64
	Through       uint64
}

func messageUpdateCursorBinding(uid string, id ChannelID) string {
	// JSON encodes tuple boundaries and escaping unambiguously. This is a
	// context binding, not authentication or a replacement for membership reads.
	b, _ := json.Marshal(struct {
		UID         string
		ChannelID   string
		ChannelType uint8
	}{uid, id.ID, id.Type})
	digest := sha256.Sum256(b)
	return hex.EncodeToString(digest[:])
}

func encodeUpdateCursor(c updateCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeUpdateCursor(s string) (updateCursor, error) {
	var c updateCursor
	if len(s) > 4096 {
		return c, ErrUpdateInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil {
		return c, ErrUpdateInvalid
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, ErrUpdateInvalid
	}
	switch c.Format {
	case 1:
		if c.UID == "" || c.ChannelID == "" || c.ChannelType == 0 || c.Binding != "" {
			return c, ErrUpdateInvalid
		}
	case 2:
		if len(c.Binding) != sha256.Size*2 || c.UID != "" || c.ChannelID != "" || c.ChannelType != 0 {
			return c, ErrUpdateInvalid
		}
		if _, e = hex.DecodeString(c.Binding); e != nil {
			return c, ErrUpdateInvalid
		}
	default:
		return c, ErrUpdateInvalid
	}
	return c, nil
}

// MessageUpdates obtains latest edit states through the channel index, then
// checks each original through the existing committed/visibility read path.
func (a *App) MessageUpdates(ctx context.Context, q MessageUpdatesQuery) (MessageUpdatesResult, error) {
	out := MessageUpdatesResult{Updates: []SyncedMessage{}}
	if a == nil || a.updates == nil || a.lookupReader == nil {
		return out, ErrUpdateUnavailable
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	if q.Limit < 1 || q.Limit > metadb.MaxMessageUpdatePage {
		return out, ErrUpdateInvalid
	}
	if a.commandChannels.IsCommandChannel(strings.TrimSpace(q.ChannelID)) {
		return out, ErrNotUpdatable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	epoch := uint64(0)
	if a.contentEpoch != nil {
		var e error
		epoch, e = a.contentEpoch(ctx)
		if e != nil {
			return out, e
		}
	}
	normalized, err := a.normalizeSyncChannelMessages(SyncChannelMessagesQuery{LoginUID: q.LoginUID, ChannelID: q.ChannelID, ChannelType: q.ChannelType})
	if err != nil {
		return out, err
	}
	membership, found, err := a.memberships.GetUserChannelMembership(ctx, normalized.LoginUID, normalized.ChannelID, int64(normalized.ChannelType))
	if err != nil {
		return out, err
	}
	prepared, err := prepareSyncMembership(normalized, membership, found)
	if err != nil {
		return out, err
	}
	id := ChannelID{ID: normalized.ChannelID, Type: normalized.ChannelType}
	binding := messageUpdateCursorBinding(normalized.LoginUID, id)
	var cursor updateCursor
	if q.UpdateCursor != "" {
		cursor, err = decodeUpdateCursor(q.UpdateCursor)
		if err != nil {
			return out, err
		}
		if cursor.Format == 1 {
			if cursor.UID != normalized.LoginUID || cursor.ChannelID != id.ID || cursor.ChannelType != id.Type {
				return out, ErrUpdateInvalid
			}
		} else if cursor.Binding != binding {
			return out, ErrUpdateInvalid
		}
	}
	if prepared.empty {
		// A new person channel has no visible history until its directory projection exists.
		baseline := updateCursor{RestoreEpoch: epoch, Format: 2, Binding: binding}
		out.NextUpdateCursor = encodeUpdateCursor(baseline)
		out.ResetRequired = q.UpdateCursor != out.NextUpdateCursor
		return out, nil
	}
	if a.channelState != nil {
		channel, e := a.channelState.GetChannelForMessagePull(ctx, id.ID, int64(id.Type))
		if e = validateSyncChannelState(channel, e); e != nil {
			return out, e
		}
	}
	head, err := a.updateHead(ctx, id)
	if err != nil {
		return out, err
	}
	current := updateCursor{RestoreEpoch: epoch, Format: 2, Binding: binding, Generation: head.Generation, SourceVersion: membership.SourceVersion, JoinSeq: membership.JoinSeq, DeletedToSeq: membership.DeletedToSeq, After: head.UpdateSeq}
	reset := func() (MessageUpdatesResult, error) {
		out.ResetRequired = true
		out.NextUpdateCursor = encodeUpdateCursor(current)
		return out, nil
	}
	if q.UpdateCursor == "" || cursor.Format == 1 {
		return reset()
	}
	if cursor.RestoreEpoch != current.RestoreEpoch || cursor.Generation != current.Generation || cursor.SourceVersion != current.SourceVersion || cursor.JoinSeq != current.JoinSeq || cursor.DeletedToSeq != current.DeletedToSeq || cursor.After > head.UpdateSeq || cursor.Through > head.UpdateSeq {
		return reset()
	}
	pages, err := a.updates.ReadMessageUpdatesBatch(ctx, []metadb.MessageUpdateRead{{ChannelID: id.ID, ChannelType: int64(id.Type), After: cursor.After, Through: cursor.Through, Limit: q.Limit}})
	if err != nil {
		return out, err
	}
	if len(pages) != 1 {
		return out, ErrUpdateUnavailable
	}
	page := pages[0]
	if page.Head.Generation != head.Generation || page.Head.UpdateSeq < head.UpdateSeq || cursor.After > page.Head.UpdateSeq {
		current.Generation = page.Head.Generation
		current.After = page.Head.UpdateSeq
		return reset()
	}
	used := 0
	for start := 0; start < len(page.Updates); start += 8 {
		rows := page.Updates[start:min(start+8, len(page.Updates))]
		reads := make([]MessageScanQuery, len(rows))
		for i, row := range rows {
			reads[i] = MessageScanQuery{ChannelID: id, MessageID: row.MessageID, MinSeq: prepared.query.MinSeq, Limit: 1, MaxBytes: metadb.MaxMessageUpdatePayload}
		}
		originals, e := a.lookupReader.ReadCommittedMessages(ctx, reads)
		if e != nil {
			return out, e
		}
		if len(originals) != len(rows) {
			return out, ErrUpdateUnavailable
		}
		for i, base := range originals {
			if base.Err != nil {
				if errors.Is(base.Err, ErrChannelNotFound) || errors.Is(base.Err, metadb.ErrNotFound) {
					continue
				}
				return out, base.Err
			}
			if len(base.Messages) == 0 {
				continue
			}
			if len(base.Messages) != 1 {
				return out, ErrUpdateUnavailable
			}
			msg := base.Messages[0]
			row := rows[i]
			if msg.MessageID != row.MessageID || msg.MessageSeq != row.MessageSeq || msg.Flags.SyncOnce || msg.Flags.NoPersist || isLegacyStreamMessage(msg.Setting) {
				continue
			}
			// A subsequent read may already contain a newer committed replacement.
			if msg.Version <= row.Version {
				msg.Payload = row.Payload
				msg.Version = row.Version
				msg.UpdatedAtMS = row.UpdatedAtMS
			}
			size := len(msg.Payload) + len(msg.ChannelID) + 128
			if used+size > metadb.MaxMessageUpdatePageBytes {
				// Advance only through the preceding indexed target, including
				// filtered originals. A moved target is caught by the next round.
				cursor.After = row.UpdateSeq - 1
				cursor.Through = page.Through
				out.More = true
				out.NextUpdateCursor = encodeUpdateCursor(cursor)
				return out, nil
			}
			used += size
			out.Updates = append(out.Updates, msg)
		}
	}
	cursor.After = page.Next
	cursor.Through = 0
	if page.More {
		cursor.Through = page.Through
	}
	out.More = page.More
	out.NextUpdateCursor = encodeUpdateCursor(cursor)
	return out, nil
}

// MessageUpdateHint is a body-free notification, not a new chat message.
type MessageUpdateHint struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
	MessageID   uint64 `json:"message_id,string"`
	MessageSeq  uint64 `json:"message_seq,string"`
	Version     uint64 `json:"version,string"`
}

// UpdateHintSender routes one bounded subscriber page to capable online devices.
type UpdateHintSender interface {
	SendMessageUpdateHint(context.Context, []string, MessageUpdateHint) error
}

// UpdateSubscribers pages source-channel subscribers through Slot authority.
type UpdateSubscribers interface {
	ListChannelSubscribersAuthoritative(context.Context, string, int64, string, int) ([]string, string, bool, error)
}

// DispatchMessageUpdate advances at most one subscriber page. Durable progress
// and conditional completion prevent retry work from growing with group size.
func (a *App) DispatchMessageUpdate(ctx context.Context, task metadb.MessageUpdate) (bool, error) {
	if a.updateHints == nil || a.updateSubscribers == nil {
		return false, ErrUpdateUnavailable
	}
	id := ChannelID{ID: task.ChannelID, Type: uint8(task.ChannelType)}
	pages, err := a.updates.ReadMessageUpdatesBatch(ctx, []metadb.MessageUpdateRead{{ChannelID: id.ID, ChannelType: task.ChannelType, IDs: []uint64{task.MessageID}, IncludePending: true}})
	if err != nil {
		return false, err
	}
	if len(pages) != 1 {
		return false, ErrUpdateUnavailable
	}
	page := pages[0]
	if len(page.Updates) == 0 {
		return false, nil
	}
	latest := page.Updates[0]
	if latest.Version != task.Version || latest.Pending == 0 {
		return false, nil
	}
	// Pending rows are created only for validated ordinary originals. Hints
	// convey no content; the authoritative retention floor suppresses reclaimed
	// targets without reading a large body for every subscriber page.
	runtime, err := a.updates.GetChannelRuntimeMeta(ctx, id.ID, task.ChannelType)
	if err != nil && !errors.Is(err, metadb.ErrNotFound) {
		return false, err
	}
	visible := err == nil && latest.MessageSeq > runtime.RetentionThroughSeq
	var uids []string
	var next string
	done := true
	if visible {
		if id.Type == 1 {
			left, right, e := runtimechannelid.DecodePersonChannel(id.ID)
			if e != nil {
				return false, e
			}
			uids = []string{left, right}
		} else {
			uids, next, done, err = a.updateSubscribers.ListChannelSubscribersAuthoritative(ctx, id.ID, task.ChannelType, latest.PendingAfterUID, 128)
			if err != nil {
				return false, err
			}
		}
		if err = a.updateHints.SendMessageUpdateHint(ctx, uids, MessageUpdateHint{ChannelID: id.ID, ChannelType: id.Type, MessageID: task.MessageID, MessageSeq: task.MessageSeq, Version: task.Version}); err != nil {
			return false, err
		}
	}
	op := "progress"
	if done {
		op = "ack"
	} else if next == "" || next == latest.PendingAfterUID {
		return false, ErrUpdateUnavailable
	}
	result, err := a.updates.ApplyMessageUpdate(ctx, metadb.MessageUpdateMutation{Op: op, ChannelID: id.ID, ChannelType: task.ChannelType, Generation: page.Head.Generation, ReplicaSet: page.Head.ReplicaSet, MessageID: task.MessageID, ExpectedVersion: task.Version, AfterUID: next, PreviousUID: latest.PendingAfterUID})
	if err != nil {
		return false, err
	}
	if result.Status != "ok" && result.Status != "message_not_found" && result.Status != "reset_required" {
		return false, UpdateError(result.Status)
	}
	return !done, nil
}

// PruneMessageUpdate submits cleanup only after the retention floor passes the
// original. Slot apply rechecks that floor and the channel incarnation.
func (a *App) PruneMessageUpdate(ctx context.Context, target metadb.MessageUpdate) error {
	runtime, err := a.updates.GetChannelRuntimeMeta(ctx, target.ChannelID, target.ChannelType)
	if errors.Is(err, metadb.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if target.MessageSeq > runtime.RetentionThroughSeq {
		return nil
	}
	pages, err := a.updates.ReadMessageUpdatesBatch(ctx, []metadb.MessageUpdateRead{{ChannelID: target.ChannelID, ChannelType: target.ChannelType}})
	if err != nil {
		return err
	}
	if len(pages) != 1 {
		return ErrUpdateUnavailable
	}
	if pages[0].Head.Generation == "" {
		return nil
	}
	_, err = a.updates.ApplyMessageUpdate(ctx, metadb.MessageUpdateMutation{Op: "prune", ChannelID: target.ChannelID, ChannelType: target.ChannelType, MessageID: target.MessageID, Generation: pages[0].Head.Generation, ReplicaSet: pages[0].Head.ReplicaSet})
	return err
}
