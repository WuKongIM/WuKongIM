package transfer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// ValidateBundle streams an import bundle and validates its semantic invariants without writing storage.
func ValidateBundle(ctx context.Context, root string, opts ImportOptions) (ImportStats, error) {
	manifest, err := LoadManifest(root)
	if err != nil {
		return ImportStats{}, err
	}

	hashSlotCount, err := validateHashSlotCount(manifest, opts)
	if err != nil {
		return ImportStats{}, err
	}

	var stats ImportStats
	validator := newBundleValidator(hashSlotCount)
	for _, entry := range manifest.Files {
		rel, err := safeBundlePath(entry.Path)
		if err != nil {
			return stats, fmt.Errorf("%w: unsafe path %q: %v", ErrValidation, entry.Path, err)
		}
		filePath := filepath.Join(root, filepath.FromSlash(rel))
		file, err := os.Open(filePath)
		if err != nil {
			return stats, fmt.Errorf("%w: open %q: %v", ErrValidation, entry.Path, err)
		}
		info, err := file.Stat()
		if err != nil {
			closeErr := file.Close()
			if closeErr != nil {
				return stats, fmt.Errorf("%w: stat %q: %v; close: %v", ErrValidation, entry.Path, err, closeErr)
			}
			return stats, fmt.Errorf("%w: stat %q: %v", ErrValidation, entry.Path, err)
		}
		if !info.Mode().IsRegular() {
			closeErr := file.Close()
			if closeErr != nil {
				return stats, fmt.Errorf("%w: %q non-regular file; close: %v", ErrValidation, entry.Path, closeErr)
			}
			return stats, fmt.Errorf("%w: %q non-regular file", ErrValidation, entry.Path)
		}

		stats.Files++
		stats.BytesRead += info.Size()
		err = readJSONL(ctx, file, entry.Kind, func(record any) error {
			if err := validator.Visit(entry.Kind, record); err != nil {
				return err
			}
			stats.RowsValidated++
			return nil
		})
		closeErr := file.Close()
		if err != nil {
			return stats, fmt.Errorf("validate %q: %w", entry.Path, err)
		}
		if closeErr != nil {
			return stats, fmt.Errorf("%w: close %q: %v", ErrValidation, entry.Path, closeErr)
		}
	}
	if err := validator.Finish(); err != nil {
		return stats, err
	}
	return stats, nil
}

func validateHashSlotCount(manifest Manifest, opts ImportOptions) (uint16, error) {
	if manifest.HashSlotCount > int(^uint16(0)) {
		return 0, fmt.Errorf("%w: hash_slot_count %d exceeds uint16", ErrValidation, manifest.HashSlotCount)
	}
	if opts.HashSlotCount != 0 && int(opts.HashSlotCount) != manifest.HashSlotCount {
		return 0, fmt.Errorf("%w: hash_slot_count mismatch manifest=%d target=%d", ErrValidation, manifest.HashSlotCount, opts.HashSlotCount)
	}
	return uint16(manifest.HashSlotCount), nil
}

type bundleValidator struct {
	hashSlotCount uint16

	channels map[string]MessageChannelRecord

	haveSubscriberOrder bool
	subscriberOrder     subscriberOrder
	subscriberHashErr   error

	haveMessageOrder bool
	messageOrder     messageOrder
}

func newBundleValidator(hashSlotCount uint16) *bundleValidator {
	return &bundleValidator{
		hashSlotCount: hashSlotCount,
		channels:      make(map[string]MessageChannelRecord),
	}
}

func (v *bundleValidator) Visit(kind FileKind, record any) error {
	switch kind {
	case FileKindMetaUsers:
		row := record.(UserRecord)
		return v.validateHashSlot("users", row.UID, row.HashSlot)
	case FileKindMetaDevices:
		row := record.(DeviceRecord)
		return v.validateHashSlot("devices", row.UID, row.HashSlot)
	case FileKindMetaChannels:
		row := record.(ChannelRecord)
		return v.validateHashSlot("channels", row.ChannelID, row.HashSlot)
	case FileKindMetaSubscribers:
		row := record.(SubscriberRecord)
		if err := v.validateSubscriberOrder(row); err != nil {
			return err
		}
		if err := v.validateHashSlot("subscribers", row.ChannelID, row.HashSlot); err != nil && v.subscriberHashErr == nil {
			v.subscriberHashErr = err
		}
		return nil
	case FileKindMetaUserChannelMemberships:
		row := record.(UserChannelMembershipRecord)
		return v.validateHashSlot("user_channel_memberships", row.UID, row.HashSlot)
	case FileKindMetaConversations:
		row := record.(ConversationRecord)
		return v.validateHashSlot("conversations", row.UID, row.HashSlot)
	case FileKindMetaChannelLatest:
		row := record.(ChannelLatestRecord)
		return v.validateHashSlot("channel_latest", row.ChannelID, row.HashSlot)
	case FileKindMessageChannels:
		row := record.(MessageChannelRecord)
		return v.validateMessageChannel(row)
	case FileKindMessageMessages:
		row := record.(MessageRecord)
		return v.validateMessage(row)
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrValidation, kind)
	}
}

func (v *bundleValidator) Finish() error {
	if v.subscriberHashErr != nil {
		return v.subscriberHashErr
	}
	return nil
}

func (v *bundleValidator) validateHashSlot(dataset, key string, got uint16) error {
	want := cluster.HashSlotForKey(key, v.hashSlotCount)
	if got != want {
		return fmt.Errorf("%w: %s hash_slot mismatch key=%q got=%d want=%d", ErrValidation, dataset, key, got, want)
	}
	return nil
}

func (v *bundleValidator) validateSubscriberOrder(row SubscriberRecord) error {
	current := subscriberOrder{
		hashSlot:    row.HashSlot,
		channelID:   row.ChannelID,
		channelType: row.ChannelType,
		uid:         row.UID,
	}
	if v.haveSubscriberOrder && compareSubscriberOrder(v.subscriberOrder, current) > 0 {
		return fmt.Errorf("%w: subscriber order violation previous=(%d,%q,%d,%q) current=(%d,%q,%d,%q)",
			ErrValidation,
			v.subscriberOrder.hashSlot,
			v.subscriberOrder.channelID,
			v.subscriberOrder.channelType,
			v.subscriberOrder.uid,
			current.hashSlot,
			current.channelID,
			current.channelType,
			current.uid,
		)
	}
	v.haveSubscriberOrder = true
	v.subscriberOrder = current
	return nil
}

func (v *bundleValidator) validateMessageChannel(row MessageChannelRecord) error {
	if _, ok := v.channels[row.ChannelKey]; ok {
		return fmt.Errorf("%w: duplicate message channel channel_key=%q", ErrValidation, row.ChannelKey)
	}
	v.channels[row.ChannelKey] = row
	return nil
}

func (v *bundleValidator) validateMessage(row MessageRecord) error {
	if _, ok := v.channels[row.ChannelKey]; !ok {
		return fmt.Errorf("%w: missing message channel channel_key=%q", ErrValidation, row.ChannelKey)
	}
	if !v.haveMessageOrder || row.ChannelKey != v.messageOrder.channelKey {
		if v.haveMessageOrder && row.ChannelKey < v.messageOrder.channelKey {
			return fmt.Errorf("%w: message order violation previous_channel_key=%q current_channel_key=%q", ErrValidation, v.messageOrder.channelKey, row.ChannelKey)
		}
		v.haveMessageOrder = true
		v.messageOrder = newMessageOrder(row.ChannelKey)
	}
	return v.messageOrder.visit(row)
}

type subscriberOrder struct {
	hashSlot    uint16
	channelID   string
	channelType int64
	uid         string
}

func compareSubscriberOrder(left, right subscriberOrder) int {
	if left.hashSlot != right.hashSlot {
		if left.hashSlot < right.hashSlot {
			return -1
		}
		return 1
	}
	if left.channelID != right.channelID {
		if left.channelID < right.channelID {
			return -1
		}
		return 1
	}
	if left.channelType != right.channelType {
		if left.channelType < right.channelType {
			return -1
		}
		return 1
	}
	if left.uid != right.uid {
		if left.uid < right.uid {
			return -1
		}
		return 1
	}
	return 0
}

type messageOrder struct {
	channelKey      string
	nextSeq         uint64
	messageIDs      map[uint64]struct{}
	idempotencyKeys map[string]struct{}
}

func newMessageOrder(channelKey string) messageOrder {
	return messageOrder{
		channelKey:      channelKey,
		nextSeq:         1,
		messageIDs:      make(map[uint64]struct{}),
		idempotencyKeys: make(map[string]struct{}),
	}
}

func (o *messageOrder) visit(row MessageRecord) error {
	seq := uint64(row.MessageSeq)
	if seq != o.nextSeq {
		return fmt.Errorf("%w: message sequence must be contiguous channel_key=%q got=%d want=%d", ErrValidation, row.ChannelKey, seq, o.nextSeq)
	}
	o.nextSeq++

	messageID := uint64(row.MessageID)
	if _, ok := o.messageIDs[messageID]; ok {
		return fmt.Errorf("%w: duplicate message_id channel_key=%q message_id=%d", ErrValidation, row.ChannelKey, messageID)
	}
	o.messageIDs[messageID] = struct{}{}

	if row.FromUID != "" && row.ClientMsgNo != "" {
		key := row.FromUID + "\x00" + row.ClientMsgNo
		if _, ok := o.idempotencyKeys[key]; ok {
			return fmt.Errorf("%w: duplicate idempotency channel_key=%q from_uid=%q client_msg_no=%q", ErrValidation, row.ChannelKey, row.FromUID, row.ClientMsgNo)
		}
		o.idempotencyKeys[key] = struct{}{}
	}
	return nil
}
