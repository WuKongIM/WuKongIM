package transfer

import (
	"bufio"
	"bytes"
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
	var nonMessageEntries []FileEntry
	var messageChannelEntries []FileEntry
	var messageEntries []FileEntry
	for _, entry := range manifest.Files {
		switch entry.Kind {
		case FileKindMessageChannels:
			messageChannelEntries = append(messageChannelEntries, entry)
		case FileKindMessageMessages:
			messageEntries = append(messageEntries, entry)
		default:
			nonMessageEntries = append(nonMessageEntries, entry)
		}
	}

	validator := newBundleValidator(hashSlotCount)
	for _, entry := range nonMessageEntries {
		if err := validateBundleEntry(ctx, root, entry, &stats, func(record any) error {
			if err := validator.Visit(entry.Kind, record); err != nil {
				return err
			}
			return nil
		}); err != nil {
			return stats, err
		}
	}
	if err := validateMessageEntries(ctx, root, messageChannelEntries, messageEntries, &stats); err != nil {
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

	haveSubscriberOrder bool
	subscriberOrder     subscriberOrder
}

func newBundleValidator(hashSlotCount uint16) *bundleValidator {
	return &bundleValidator{
		hashSlotCount: hashSlotCount,
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
		if err := v.validateHashSlot("subscribers", row.ChannelID, row.HashSlot); err != nil {
			return err
		}
		return v.validateSubscriberOrder(row)
	case FileKindMetaUserChannelMemberships:
		row := record.(UserChannelMembershipRecord)
		return v.validateHashSlot("user_channel_memberships", row.UID, row.HashSlot)
	case FileKindMetaConversations:
		row := record.(ConversationRecord)
		return v.validateHashSlot("conversations", row.UID, row.HashSlot)
	case FileKindMetaChannelLatest:
		row := record.(ChannelLatestRecord)
		return v.validateHashSlot("channel_latest", row.ChannelID, row.HashSlot)
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrValidation, kind)
	}
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

func validateBundleEntry(ctx context.Context, root string, entry FileEntry, stats *ImportStats, visit func(any) error) error {
	file, size, err := openBundleFile(root, entry)
	if err != nil {
		return err
	}

	stats.Files++
	stats.BytesRead += size
	var fileRows int64
	err = readJSONL(ctx, file, entry.Kind, func(record any) error {
		if err := visit(record); err != nil {
			return err
		}
		fileRows++
		stats.RowsValidated++
		return nil
	})
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("validate %q: %w", entry.Path, err)
	}
	if closeErr != nil {
		return fmt.Errorf("%w: close %q: %v", ErrValidation, entry.Path, closeErr)
	}
	if err := validateFileRowCount(entry, fileRows); err != nil {
		return err
	}
	return nil
}

func openBundleFile(root string, entry FileEntry) (*os.File, int64, error) {
	rel, err := safeBundlePath(entry.Path)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: unsafe path %q: %v", ErrValidation, entry.Path, err)
	}
	filePath := filepath.Join(root, filepath.FromSlash(rel))
	file, err := os.Open(filePath)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: open %q: %v", ErrValidation, entry.Path, err)
	}
	info, err := file.Stat()
	if err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return nil, 0, fmt.Errorf("%w: stat %q: %v; close: %v", ErrValidation, entry.Path, err, closeErr)
		}
		return nil, 0, fmt.Errorf("%w: stat %q: %v", ErrValidation, entry.Path, err)
	}
	if !info.Mode().IsRegular() {
		closeErr := file.Close()
		if closeErr != nil {
			return nil, 0, fmt.Errorf("%w: %q non-regular file; close: %v", ErrValidation, entry.Path, closeErr)
		}
		return nil, 0, fmt.Errorf("%w: %q non-regular file", ErrValidation, entry.Path)
	}
	return file, info.Size(), nil
}

func validateFileRowCount(entry FileEntry, got int64) error {
	if got != entry.Rows {
		return fmt.Errorf("%w: row count mismatch for %q got=%d want=%d", ErrValidation, entry.Path, got, entry.Rows)
	}
	return nil
}

func validateMessageEntries(ctx context.Context, root string, channelEntries, messageEntries []FileEntry, stats *ImportStats) error {
	channels := newMessageChannelStream(ctx, root, channelEntries, stats)
	defer channels.Close()

	validator := newMessageValidator(channels)
	for _, entry := range messageEntries {
		if err := validateBundleEntry(ctx, root, entry, stats, func(record any) error {
			return validator.Visit(record.(MessageRecord))
		}); err != nil {
			return err
		}
	}
	if err := channels.Drain(); err != nil {
		return err
	}
	return nil
}

type messageChannelStream struct {
	ctx     context.Context
	root    string
	entries []FileEntry
	stats   *ImportStats

	entryIndex int
	entry      FileEntry
	file       *os.File
	scanner    *bufio.Scanner
	lineNo     int
	fileRows   int64

	current *MessageChannelRecord
	lastKey string
	haveKey bool
}

func newMessageChannelStream(ctx context.Context, root string, entries []FileEntry, stats *ImportStats) *messageChannelStream {
	return &messageChannelStream{
		ctx:     ctx,
		root:    root,
		entries: entries,
		stats:   stats,
	}
}

func (s *messageChannelStream) Declared(channelKey string) (bool, error) {
	for {
		row, ok, err := s.Peek()
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		if row.ChannelKey >= channelKey {
			return row.ChannelKey == channelKey, nil
		}
		s.current = nil
	}
}

func (s *messageChannelStream) Drain() error {
	for {
		_, ok, err := s.Peek()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		s.current = nil
	}
}

func (s *messageChannelStream) Close() error {
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	s.scanner = nil
	return err
}

func (s *messageChannelStream) Peek() (MessageChannelRecord, bool, error) {
	if s.current != nil {
		return *s.current, true, nil
	}
	for {
		if s.scanner == nil {
			if err := s.openNextFile(); err != nil {
				return MessageChannelRecord{}, false, err
			}
			if s.scanner == nil {
				return MessageChannelRecord{}, false, nil
			}
		}
		for s.scanner.Scan() {
			s.lineNo++
			if err := s.ctx.Err(); err != nil {
				return MessageChannelRecord{}, false, err
			}
			line := bytes.TrimSpace(s.scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			record, err := decodeRecord(FileKindMessageChannels, line)
			if err != nil {
				return MessageChannelRecord{}, false, fmt.Errorf("validate %q: line %d: %w", s.entry.Path, s.lineNo, err)
			}
			row := record.(MessageChannelRecord)
			if err := s.validateOrder(row); err != nil {
				return MessageChannelRecord{}, false, fmt.Errorf("validate %q: line %d: %w", s.entry.Path, s.lineNo, err)
			}
			s.fileRows++
			s.stats.RowsValidated++
			s.current = &row
			return row, true, nil
		}
		if err := s.scanner.Err(); err != nil {
			return MessageChannelRecord{}, false, fmt.Errorf("validate %q: scan jsonl: %w", s.entry.Path, err)
		}
		if err := s.ctx.Err(); err != nil {
			return MessageChannelRecord{}, false, err
		}
		if err := validateFileRowCount(s.entry, s.fileRows); err != nil {
			return MessageChannelRecord{}, false, err
		}
		if err := s.Close(); err != nil {
			return MessageChannelRecord{}, false, fmt.Errorf("%w: close %q: %v", ErrValidation, s.entry.Path, err)
		}
	}
}

func (s *messageChannelStream) openNextFile() error {
	if s.entryIndex >= len(s.entries) {
		return nil
	}
	entry := s.entries[s.entryIndex]
	s.entryIndex++

	file, size, err := openBundleFile(s.root, entry)
	if err != nil {
		return err
	}
	s.entry = entry
	s.file = file
	s.scanner = bufio.NewScanner(file)
	s.scanner.Buffer(make([]byte, 0, 64*1024), maxJSONLLineBytes)
	s.lineNo = 0
	s.fileRows = 0
	s.stats.Files++
	s.stats.BytesRead += size
	return nil
}

func (s *messageChannelStream) validateOrder(row MessageChannelRecord) error {
	if !s.haveKey {
		s.haveKey = true
		s.lastKey = row.ChannelKey
		return nil
	}
	if row.ChannelKey == s.lastKey {
		return fmt.Errorf("%w: duplicate message channel channel_key=%q", ErrValidation, row.ChannelKey)
	}
	if row.ChannelKey < s.lastKey {
		return fmt.Errorf("%w: message channel order violation previous_channel_key=%q current_channel_key=%q", ErrValidation, s.lastKey, row.ChannelKey)
	}
	s.lastKey = row.ChannelKey
	return nil
}

type messageValidator struct {
	channels *messageChannelStream

	haveMessageOrder bool
	messageOrder     messageOrder
}

func newMessageValidator(channels *messageChannelStream) *messageValidator {
	return &messageValidator{channels: channels}
}

func (v *messageValidator) Visit(row MessageRecord) error {
	if !v.haveMessageOrder || row.ChannelKey != v.messageOrder.channelKey {
		if v.haveMessageOrder && row.ChannelKey < v.messageOrder.channelKey {
			return fmt.Errorf("%w: message order violation previous_channel_key=%q current_channel_key=%q", ErrValidation, v.messageOrder.channelKey, row.ChannelKey)
		}
		declared, err := v.channels.Declared(row.ChannelKey)
		if err != nil {
			return err
		}
		if !declared {
			return fmt.Errorf("%w: missing message channel channel_key=%q", ErrValidation, row.ChannelKey)
		}
		v.haveMessageOrder = true
		v.messageOrder = newMessageOrder(row.ChannelKey)
	}
	return v.messageOrder.visit(row)
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
