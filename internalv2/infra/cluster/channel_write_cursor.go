package cluster

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/channelwrite"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ChannelWritePostCommitCursorMetadataKey is the app-local channel metadata key for post-commit progress.
const ChannelWritePostCommitCursorMetadataKey = "internalv2.channelwrite.post_commit_cursor"

// ChannelWriteCursorMetadataNode exposes app-local channel metadata for channelwrite cursors.
type ChannelWriteCursorMetadataNode interface {
	// LoadChannelAppMetadata loads one app-local value scoped to a channel.
	LoadChannelAppMetadata(context.Context, string, int64, string) ([]byte, error)
	// StoreChannelAppMetadata stores one app-local value scoped to a channel.
	StoreChannelAppMetadata(context.Context, string, int64, string, []byte) error
}

// ChannelWriteCursorStore persists channelwrite post-commit cursors in channel metadata.
type ChannelWriteCursorStore struct {
	node ChannelWriteCursorMetadataNode
	key  string
}

var _ channelwrite.CursorStore = (*ChannelWriteCursorStore)(nil)

// NewChannelWriteCursorStore creates a clusterv2-backed post-commit cursor store.
func NewChannelWriteCursorStore(node ChannelWriteCursorMetadataNode) *ChannelWriteCursorStore {
	return &ChannelWriteCursorStore{node: node, key: ChannelWritePostCommitCursorMetadataKey}
}

// LoadPostCommitCursor returns the last completed post-commit sequence for a channel.
func (s *ChannelWriteCursorStore) LoadPostCommitCursor(ctx context.Context, id channelwrite.ChannelID) (uint64, error) {
	if err := contextError(ctx); err != nil {
		return 0, err
	}
	if s == nil || s.node == nil {
		return 0, channelwrite.ErrRouteNotReady
	}
	value, err := s.node.LoadChannelAppMetadata(ctx, id.ID, int64(id.Type), s.key)
	if errors.Is(err, metadb.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if len(value) == 0 {
		return 0, nil
	}
	seq, err := decodePostCommitCursor(value)
	if err != nil {
		return 0, err
	}
	return seq, nil
}

// StorePostCommitCursor stores seq only when it advances the existing channel cursor.
func (s *ChannelWriteCursorStore) StorePostCommitCursor(ctx context.Context, id channelwrite.ChannelID, seq uint64) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if s == nil || s.node == nil {
		return channelwrite.ErrRouteNotReady
	}
	current, err := s.LoadPostCommitCursor(ctx, id)
	if err != nil {
		return err
	}
	if seq <= current {
		return nil
	}
	return s.node.StoreChannelAppMetadata(ctx, id.ID, int64(id.Type), s.key, encodePostCommitCursor(seq))
}

// ChannelWriteCommittedReader reads durable channel messages for channelwrite replay.
type ChannelWriteCommittedReader struct {
	node ChannelMessageReadNode
}

var _ channelwrite.CommittedReader = (*ChannelWriteCommittedReader)(nil)

// NewChannelWriteCommittedReader creates a clusterv2-backed committed replay reader.
func NewChannelWriteCommittedReader(node ChannelMessageReadNode) *ChannelWriteCommittedReader {
	return &ChannelWriteCommittedReader{node: node}
}

// ReadCommittedFrom reads committed messages in ascending sequence order for replay.
func (r *ChannelWriteCommittedReader) ReadCommittedFrom(ctx context.Context, id channelwrite.ChannelID, fromSeq uint64, limit int) ([]channelwrite.CommittedMessage, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if r == nil || r.node == nil {
		return nil, channelwrite.ErrRouteNotReady
	}
	if fromSeq == 0 {
		fromSeq = 1
	}
	if limit <= 0 {
		limit = 1
	}
	read, err := r.node.ReadChannelCommitted(ctx, channelv2.ChannelID{ID: id.ID, Type: id.Type}, channelstore.ReadCommittedRequest{
		FromSeq:  fromSeq,
		MaxSeq:   maxUint64(),
		Limit:    limit,
		MaxBytes: maxInt(),
	})
	if err != nil {
		return nil, mapChannelWriteCursorError(err)
	}
	return channelWriteCommittedMessagesFromChannel(read.Messages), nil
}

func mapChannelWriteCursorError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case appendErrorMatches(err, channelv2.ErrChannelNotFound):
		return fmt.Errorf("%w: %w", channelwrite.ErrChannelNotFound, err)
	default:
		return mapChannelWriteRouteError(err)
	}
}

func channelWriteCommittedMessagesFromChannel(messages []channelv2.Message) []channelwrite.CommittedMessage {
	out := make([]channelwrite.CommittedMessage, 0, len(messages))
	for _, msg := range messages {
		out = append(out, channelwrite.CommittedMessage{
			MessageID:         msg.MessageID,
			MessageSeq:        msg.MessageSeq,
			ChannelID:         msg.ChannelID,
			ChannelType:       msg.ChannelType,
			FromUID:           msg.FromUID,
			ClientMsgNo:       msg.ClientMsgNo,
			ServerTimestampMS: msg.ServerTimestampMS,
			Payload:           append([]byte(nil), msg.Payload...),
		})
	}
	return out
}

func encodePostCommitCursor(seq uint64) []byte {
	return []byte(strconv.FormatUint(seq, 10))
}

func decodePostCommitCursor(value []byte) (uint64, error) {
	seq, err := strconv.ParseUint(string(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("channelwrite cursor: invalid post-commit cursor %q: %w", string(value), err)
	}
	return seq, nil
}

func channelKeyForCursorMetadata(channelID string, channelType int64, key string) string {
	return strconv.FormatInt(channelType, 10) + ":" + channelID + ":" + key
}
