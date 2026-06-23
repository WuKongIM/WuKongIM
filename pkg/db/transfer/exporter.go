package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultExportPageSize        = 1000
	defaultExportMessageFileRows = 100000
)

// ExportBundle writes a read-only WKDB store into a WKDB import bundle.
func ExportBundle(ctx context.Context, root string, store *inspect.Store, opts ExportOptions) (ExportStats, error) {
	var stats ExportStats
	if store == nil {
		return stats, fmt.Errorf("export bundle: nil source store")
	}
	opts = normalizeExportOptions(opts)
	if opts.HashSlotCount == 0 {
		return stats, fmt.Errorf("%w: export hash slot count is required", ErrValidation)
	}
	if err := prepareExportRoot(root, opts.Overwrite); err != nil {
		return stats, err
	}

	manifest := Manifest{
		Format:        bundleFormat,
		Version:       bundleVersion,
		HashSlotCount: int(opts.HashSlotCount),
	}
	metaEntries, err := exportMetaFiles(ctx, root, store.Meta(), opts, &stats)
	if err != nil {
		return stats, err
	}
	manifest.Files = append(manifest.Files, metaEntries...)
	messageEntries, err := exportMessageFiles(ctx, root, store.Messages(), opts, &stats)
	if err != nil {
		return stats, err
	}
	manifest.Files = append(manifest.Files, messageEntries...)
	if err := writeExportManifest(root, manifest); err != nil {
		return stats, err
	}
	return stats, nil
}

func normalizeExportOptions(opts ExportOptions) ExportOptions {
	if opts.PageSize <= 0 {
		opts.PageSize = defaultExportPageSize
	}
	if opts.MessageFileRows <= 0 {
		opts.MessageFileRows = defaultExportMessageFileRows
	}
	return opts
}

func prepareExportRoot(root string, overwrite bool) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("export bundle: output path is required")
	}
	clean := filepath.Clean(root)
	if clean == "." || clean == string(os.PathSeparator) {
		return fmt.Errorf("export bundle: refusing unsafe output path %q", root)
	}
	info, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return os.MkdirAll(root, 0o755)
		}
		return fmt.Errorf("export bundle: stat output path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("export bundle: output path must not be a symlink")
	}
	if !info.IsDir() {
		return fmt.Errorf("export bundle: output path must be a directory")
	}
	if overwrite {
		if err := os.RemoveAll(root); err != nil {
			return fmt.Errorf("export bundle: remove output path: %w", err)
		}
		return os.MkdirAll(root, 0o755)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("export bundle: read output directory: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("export bundle: output directory is not empty")
	}
	return nil
}

type exportMetaSpec struct {
	table   string
	path    string
	kind    FileKind
	convert func(uint16, metadb.InspectRow) (any, error)
	count   func(*ExportStats)
}

func exportMetaFiles(ctx context.Context, root string, meta *metadb.MetaDB, opts ExportOptions, stats *ExportStats) ([]FileEntry, error) {
	if meta == nil {
		return nil, fmt.Errorf("export bundle: metadata store is not open")
	}
	specs := []exportMetaSpec{
		{table: "user", path: "meta/users.jsonl", kind: FileKindMetaUsers, convert: exportUserRecord},
		{table: "device", path: "meta/devices.jsonl", kind: FileKindMetaDevices, convert: exportDeviceRecord},
		{table: "channel", path: "meta/channels.jsonl", kind: FileKindMetaChannels, convert: exportChannelRecord, count: func(s *ExportStats) { s.ChannelsExported++ }},
		{table: "subscriber", path: "meta/subscribers.jsonl", kind: FileKindMetaSubscribers, convert: exportSubscriberRecord, count: func(s *ExportStats) { s.SubscribersExported++ }},
		{table: "user_channel_membership", path: "meta/memberships.jsonl", kind: FileKindMetaUserChannelMemberships, convert: exportUserChannelMembershipRecord},
		{table: "conversation", path: "meta/conversations.jsonl", kind: FileKindMetaConversations, convert: exportConversationRecord},
		{table: "channel_latest", path: "meta/channel_latest.jsonl", kind: FileKindMetaChannelLatest, convert: exportChannelLatestRecord},
	}

	entries := make([]FileEntry, 0, len(specs))
	for _, spec := range specs {
		entry, err := exportMetaFile(ctx, root, meta, opts, spec, stats)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func exportMetaFile(ctx context.Context, root string, meta *metadb.MetaDB, opts ExportOptions, spec exportMetaSpec, stats *ExportStats) (FileEntry, error) {
	writer, err := newExportFileWriter(root, spec.path, spec.kind)
	if err != nil {
		return FileEntry{}, err
	}
	for slot := uint16(0); slot < opts.HashSlotCount; slot++ {
		var after *metadb.InspectCursor
		for {
			result, err := metadb.InspectScan(ctx, meta, metadb.InspectScanRequest{
				Table:       spec.table,
				HashSlot:    slot,
				HashSlotSet: true,
				After:       after,
				Limit:       opts.PageSize,
			})
			if err != nil {
				_, _ = writer.Close()
				return FileEntry{}, fmt.Errorf("export %s slot %d: %w", spec.table, slot, err)
			}
			for _, row := range result.Rows {
				record, err := spec.convert(slot, row)
				if err != nil {
					_, _ = writer.Close()
					return FileEntry{}, fmt.Errorf("export %s slot %d: %w", spec.table, slot, err)
				}
				if err := writer.Write(record); err != nil {
					_, _ = writer.Close()
					return FileEntry{}, err
				}
				stats.RowsExported++
				if spec.count != nil {
					spec.count(stats)
				}
			}
			if result.Done {
				break
			}
			if result.Next == nil {
				_, _ = writer.Close()
				return FileEntry{}, fmt.Errorf("export %s slot %d: missing inspect cursor", spec.table, slot)
			}
			after = result.Next
		}
	}
	entry, err := writer.Close()
	if err != nil {
		return FileEntry{}, err
	}
	stats.FilesWritten++
	stats.BytesWritten += entryFileSize(root, entry.Path)
	return entry, nil
}

func exportMessageFiles(ctx context.Context, root string, messages *msgdb.MessageDB, opts ExportOptions, stats *ExportStats) ([]FileEntry, error) {
	if messages == nil {
		return nil, fmt.Errorf("export bundle: message store is not open")
	}
	channelWriter, err := newExportFileWriter(root, "message/channels.jsonl", FileKindMessageChannels)
	if err != nil {
		return nil, err
	}
	messageFiles := newExportMessageFileSet(root, opts.MessageFileRows, stats)

	var afterChannelKey string
	for {
		result, err := msgdb.InspectChannels(ctx, messages, msgdb.InspectMessageRequest{
			AfterChannelKey: afterChannelKey,
			Limit:           opts.PageSize,
		})
		if err != nil {
			_, _ = channelWriter.Close()
			_, _ = messageFiles.Close()
			return nil, fmt.Errorf("export message channels: %w", err)
		}
		for _, row := range result.Rows {
			record, err := exportMessageChannelRecord(row)
			if err != nil {
				_, _ = channelWriter.Close()
				_, _ = messageFiles.Close()
				return nil, err
			}
			if err := channelWriter.Write(record); err != nil {
				_, _ = channelWriter.Close()
				_, _ = messageFiles.Close()
				return nil, err
			}
			stats.RowsExported++
			if err := exportChannelMessages(ctx, messages, record.ChannelKey, opts, messageFiles); err != nil {
				_, _ = channelWriter.Close()
				_, _ = messageFiles.Close()
				return nil, err
			}
		}
		if result.Done {
			break
		}
		if result.Next == nil || result.Next.AfterChannelKey == "" {
			_, _ = channelWriter.Close()
			_, _ = messageFiles.Close()
			return nil, fmt.Errorf("export message channels: missing inspect cursor")
		}
		afterChannelKey = result.Next.AfterChannelKey
	}

	channelEntry, err := channelWriter.Close()
	if err != nil {
		_, _ = messageFiles.Close()
		return nil, err
	}
	stats.FilesWritten++
	stats.BytesWritten += entryFileSize(root, channelEntry.Path)
	messageEntries, err := messageFiles.Close()
	if err != nil {
		return nil, err
	}
	entries := make([]FileEntry, 0, 1+len(messageEntries))
	entries = append(entries, channelEntry)
	entries = append(entries, messageEntries...)
	return entries, nil
}

func exportChannelMessages(ctx context.Context, messages *msgdb.MessageDB, channelKey string, opts ExportOptions, files *exportMessageFileSet) error {
	var afterSeq uint64
	for {
		result, err := msgdb.InspectMessages(ctx, messages, msgdb.InspectMessageRequest{
			ChannelKey: channelKey,
			AfterSeq:   afterSeq,
			Limit:      opts.PageSize,
		})
		if err != nil {
			return fmt.Errorf("export message channel %q: %w", channelKey, err)
		}
		for _, row := range result.Rows {
			record, err := exportMessageRecord(channelKey, row)
			if err != nil {
				return fmt.Errorf("export message channel %q: %w", channelKey, err)
			}
			if err := files.Write(record); err != nil {
				return err
			}
		}
		if result.Done {
			return nil
		}
		if result.Next == nil {
			return fmt.Errorf("export message channel %q: missing inspect cursor", channelKey)
		}
		afterSeq = result.Next.AfterSeq
	}
}

type exportMessageFileSet struct {
	root        string
	maxRows     int
	stats       *ExportStats
	nextIndex   int
	current     *exportFileWriter
	currentRows int
	entries     []FileEntry
}

func newExportMessageFileSet(root string, maxRows int, stats *ExportStats) *exportMessageFileSet {
	return &exportMessageFileSet{root: root, maxRows: maxRows, stats: stats, nextIndex: 1}
}

func (s *exportMessageFileSet) Write(record MessageRecord) error {
	if s.current == nil || s.currentRows >= s.maxRows {
		if err := s.closeCurrent(); err != nil {
			return err
		}
		writer, err := newExportFileWriter(s.root, fmt.Sprintf("message/messages-%06d.jsonl", s.nextIndex), FileKindMessageMessages)
		if err != nil {
			return err
		}
		s.current = writer
		s.currentRows = 0
		s.nextIndex++
	}
	if err := s.current.Write(record); err != nil {
		return err
	}
	s.currentRows++
	s.stats.RowsExported++
	s.stats.MessagesExported++
	return nil
}

func (s *exportMessageFileSet) Close() ([]FileEntry, error) {
	if err := s.closeCurrent(); err != nil {
		return nil, err
	}
	return append([]FileEntry(nil), s.entries...), nil
}

func (s *exportMessageFileSet) closeCurrent() error {
	if s.current == nil {
		return nil
	}
	entry, err := s.current.Close()
	if err != nil {
		return err
	}
	s.entries = append(s.entries, entry)
	s.stats.FilesWritten++
	s.stats.BytesWritten += entryFileSize(s.root, entry.Path)
	s.current = nil
	s.currentRows = 0
	return nil
}

type exportFileWriter struct {
	root     string
	relPath  string
	kind     FileKind
	file     *os.File
	hash     hash.Hash
	encoder  *json.Encoder
	byteSink *countWriter
	rows     int64
	closed   bool
}

func newExportFileWriter(root, relPath string, kind FileKind) (*exportFileWriter, error) {
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return nil, fmt.Errorf("export bundle: create directory for %s: %w", relPath, err)
	}
	file, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("export bundle: create %s: %w", relPath, err)
	}
	h := sha256.New()
	sink := &countWriter{w: io.MultiWriter(file, h)}
	encoder := json.NewEncoder(sink)
	encoder.SetEscapeHTML(false)
	return &exportFileWriter{
		root:     root,
		relPath:  relPath,
		kind:     kind,
		file:     file,
		hash:     h,
		encoder:  encoder,
		byteSink: sink,
	}, nil
}

func (w *exportFileWriter) Write(record any) error {
	if w.closed {
		return fmt.Errorf("export bundle: write closed file %s", w.relPath)
	}
	if err := w.encoder.Encode(record); err != nil {
		return fmt.Errorf("export bundle: encode %s: %w", w.relPath, err)
	}
	w.rows++
	return nil
}

func (w *exportFileWriter) Close() (FileEntry, error) {
	entry := FileEntry{
		Path:   w.relPath,
		Kind:   w.kind,
		Rows:   w.rows,
		SHA256: hex.EncodeToString(w.hash.Sum(nil)),
	}
	if w.closed {
		return entry, nil
	}
	w.closed = true
	if err := w.file.Close(); err != nil {
		return entry, fmt.Errorf("export bundle: close %s: %w", w.relPath, err)
	}
	return entry, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

func entryFileSize(root, relPath string) int64 {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		return 0
	}
	return info.Size()
}

func writeExportManifest(root string, manifest Manifest) error {
	path := filepath.Join(root, manifestFileName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("export bundle: create manifest: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(manifest)
	closeErr := file.Close()
	if encodeErr != nil {
		return fmt.Errorf("export bundle: encode manifest: %w", encodeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("export bundle: close manifest: %w", closeErr)
	}
	return nil
}

func exportUserRecord(slot uint16, row metadb.InspectRow) (any, error) {
	uid, err := rowString(row, "uid")
	if err != nil {
		return nil, err
	}
	token, err := rowString(row, "token")
	if err != nil {
		return nil, err
	}
	deviceFlag, err := rowInt64(row, "device_flag")
	if err != nil {
		return nil, err
	}
	deviceLevel, err := rowInt64(row, "device_level")
	if err != nil {
		return nil, err
	}
	return UserRecord{HashSlot: slot, UID: uid, Token: token, DeviceFlag: deviceFlag, DeviceLevel: deviceLevel}, nil
}

func exportDeviceRecord(slot uint16, row metadb.InspectRow) (any, error) {
	uid, err := rowString(row, "uid")
	if err != nil {
		return nil, err
	}
	deviceFlag, err := rowInt64(row, "device_flag")
	if err != nil {
		return nil, err
	}
	token, err := rowString(row, "token")
	if err != nil {
		return nil, err
	}
	deviceLevel, err := rowInt64(row, "device_level")
	if err != nil {
		return nil, err
	}
	return DeviceRecord{HashSlot: slot, UID: uid, DeviceFlag: deviceFlag, Token: token, DeviceLevel: deviceLevel}, nil
}

func exportChannelRecord(slot uint16, row metadb.InspectRow) (any, error) {
	channelID, err := rowString(row, "channel_id")
	if err != nil {
		return nil, err
	}
	channelType, err := rowInt64(row, "channel_type")
	if err != nil {
		return nil, err
	}
	ban, err := rowInt64(row, "ban")
	if err != nil {
		return nil, err
	}
	disband, err := rowInt64(row, "disband")
	if err != nil {
		return nil, err
	}
	sendBan, err := rowInt64(row, "send_ban")
	if err != nil {
		return nil, err
	}
	allowStranger, err := rowInt64(row, "allow_stranger")
	if err != nil {
		return nil, err
	}
	large, err := rowInt64(row, "large")
	if err != nil {
		return nil, err
	}
	subscriberMutationVersion, err := rowUint64(row, "subscriber_mutation_version")
	if err != nil {
		return nil, err
	}
	return ChannelRecord{
		HashSlot:                  slot,
		ChannelID:                 channelID,
		ChannelType:               channelType,
		Ban:                       ban,
		Disband:                   disband,
		SendBan:                   sendBan,
		AllowStranger:             allowStranger,
		Large:                     large,
		SubscriberMutationVersion: Uint64(subscriberMutationVersion),
	}, nil
}

func exportSubscriberRecord(slot uint16, row metadb.InspectRow) (any, error) {
	channelID, err := rowString(row, "channel_id")
	if err != nil {
		return nil, err
	}
	channelType, err := rowInt64(row, "channel_type")
	if err != nil {
		return nil, err
	}
	uid, err := rowString(row, "uid")
	if err != nil {
		return nil, err
	}
	return SubscriberRecord{HashSlot: slot, ChannelID: channelID, ChannelType: channelType, UID: uid}, nil
}

func exportUserChannelMembershipRecord(slot uint16, row metadb.InspectRow) (any, error) {
	uid, err := rowString(row, "uid")
	if err != nil {
		return nil, err
	}
	channelID, err := rowString(row, "channel_id")
	if err != nil {
		return nil, err
	}
	channelType, err := rowInt64(row, "channel_type")
	if err != nil {
		return nil, err
	}
	joinSeq, err := rowUint64(row, "join_seq")
	if err != nil {
		return nil, err
	}
	updatedAt, err := rowInt64(row, "updated_at")
	if err != nil {
		return nil, err
	}
	return UserChannelMembershipRecord{HashSlot: slot, UID: uid, ChannelID: channelID, ChannelType: channelType, JoinSeq: Uint64(joinSeq), UpdatedAtMS: updatedAt}, nil
}

func exportConversationRecord(slot uint16, row metadb.InspectRow) (any, error) {
	uid, err := rowString(row, "uid")
	if err != nil {
		return nil, err
	}
	kind, err := exportConversationKind(row)
	if err != nil {
		return nil, err
	}
	channelID, err := rowString(row, "channel_id")
	if err != nil {
		return nil, err
	}
	channelType, err := rowInt64(row, "channel_type")
	if err != nil {
		return nil, err
	}
	readSeq, err := rowUint64(row, "read_seq")
	if err != nil {
		return nil, err
	}
	deletedToSeq, err := rowUint64(row, "deleted_to_seq")
	if err != nil {
		return nil, err
	}
	activeAt, err := rowInt64(row, "active_at")
	if err != nil {
		return nil, err
	}
	updatedAt, err := rowInt64(row, "updated_at")
	if err != nil {
		return nil, err
	}
	sparseActive, err := rowBool(row, "sparse_active")
	if err != nil {
		return nil, err
	}
	return ConversationRecord{
		HashSlot:     slot,
		UID:          uid,
		Kind:         kind,
		ChannelID:    channelID,
		ChannelType:  channelType,
		ReadSeq:      Uint64(readSeq),
		DeletedToSeq: Uint64(deletedToSeq),
		ActiveAt:     activeAt,
		UpdatedAt:    updatedAt,
		SparseActive: sparseActive,
	}, nil
}

func exportConversationKind(row metadb.InspectRow) (string, error) {
	kind, err := rowUint64(row, "kind")
	if err != nil {
		return "", err
	}
	switch metadb.ConversationKind(kind) {
	case metadb.ConversationKindNormal:
		return "normal", nil
	case metadb.ConversationKindCMD:
		return "cmd", nil
	default:
		return "", fmt.Errorf("%w: unknown conversation kind %d", ErrValidation, kind)
	}
}

func exportChannelLatestRecord(slot uint16, row metadb.InspectRow) (any, error) {
	channelID, err := rowString(row, "channel_id")
	if err != nil {
		return nil, err
	}
	channelType, err := rowInt64(row, "channel_type")
	if err != nil {
		return nil, err
	}
	lastMessageID, err := rowUint64(row, "last_message_id")
	if err != nil {
		return nil, err
	}
	lastMessageSeq, err := rowUint64(row, "last_message_seq")
	if err != nil {
		return nil, err
	}
	lastAt, err := rowInt64(row, "last_at")
	if err != nil {
		return nil, err
	}
	fromUID, err := rowString(row, "from_uid")
	if err != nil {
		return nil, err
	}
	clientMsgNo, err := rowString(row, "client_msg_no")
	if err != nil {
		return nil, err
	}
	payload, err := rowBytes(row, "payload")
	if err != nil {
		return nil, err
	}
	updatedAt, err := rowInt64(row, "updated_at")
	if err != nil {
		return nil, err
	}
	return ChannelLatestRecord{
		HashSlot:       slot,
		ChannelID:      channelID,
		ChannelType:    channelType,
		LastMessageID:  Uint64(lastMessageID),
		LastMessageSeq: Uint64(lastMessageSeq),
		LastAt:         lastAt,
		FromUID:        fromUID,
		ClientMsgNo:    clientMsgNo,
		LastPayloadB64: base64.StdEncoding.EncodeToString(payload),
		UpdatedAt:      updatedAt,
	}, nil
}

func exportMessageChannelRecord(row msgdb.InspectMessageRow) (MessageChannelRecord, error) {
	channelKey, err := rowString(row, "channel_key")
	if err != nil {
		return MessageChannelRecord{}, err
	}
	channelID, err := rowString(row, "channel_id")
	if err != nil {
		return MessageChannelRecord{}, err
	}
	channelType, err := rowUint8(row, "channel_type")
	if err != nil {
		return MessageChannelRecord{}, err
	}
	return MessageChannelRecord{ChannelKey: channelKey, ChannelID: channelID, ChannelType: channelType}, nil
}

func exportMessageRecord(channelKey string, row msgdb.InspectMessageRow) (MessageRecord, error) {
	messageSeq, err := rowUint64(row, "message_seq")
	if err != nil {
		return MessageRecord{}, err
	}
	messageID, err := rowUint64(row, "message_id")
	if err != nil {
		return MessageRecord{}, err
	}
	clientMsgNo, err := rowString(row, "client_msg_no")
	if err != nil {
		return MessageRecord{}, err
	}
	fromUID, err := rowString(row, "from_uid")
	if err != nil {
		return MessageRecord{}, err
	}
	serverTimestampMS, err := rowInt64(row, "server_timestamp_ms")
	if err != nil {
		return MessageRecord{}, err
	}
	payload, err := rowBytes(row, "payload")
	if err != nil {
		return MessageRecord{}, err
	}
	return MessageRecord{
		ChannelKey:        channelKey,
		MessageSeq:        Uint64(messageSeq),
		MessageID:         Uint64(messageID),
		ClientMsgNo:       clientMsgNo,
		FromUID:           fromUID,
		ServerTimestampMS: serverTimestampMS,
		PayloadB64:        base64.StdEncoding.EncodeToString(payload),
	}, nil
}

func rowString(row map[string]any, name string) (string, error) {
	value, ok := row[name]
	if !ok {
		return "", fmt.Errorf("%w: missing field %q", ErrValidation, name)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: field %q is %T, want string", ErrValidation, name, value)
	}
	return text, nil
}

func rowBool(row map[string]any, name string) (bool, error) {
	value, ok := row[name]
	if !ok {
		return false, fmt.Errorf("%w: missing field %q", ErrValidation, name)
	}
	out, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%w: field %q is %T, want bool", ErrValidation, name, value)
	}
	return out, nil
}

func rowBytes(row map[string]any, name string) ([]byte, error) {
	value, ok := row[name]
	if !ok {
		return nil, fmt.Errorf("%w: missing field %q", ErrValidation, name)
	}
	payload, ok := value.([]byte)
	if !ok {
		return nil, fmt.Errorf("%w: field %q is %T, want []byte", ErrValidation, name, value)
	}
	return append([]byte(nil), payload...), nil
}

func rowInt64(row map[string]any, name string) (int64, error) {
	value, ok := row[name]
	if !ok {
		return 0, fmt.Errorf("%w: missing field %q", ErrValidation, name)
	}
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case uint:
		return int64(v), nil
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint64:
		return int64(v), nil
	default:
		return 0, fmt.Errorf("%w: field %q is %T, want int64", ErrValidation, name, value)
	}
}

func rowUint64(row map[string]any, name string) (uint64, error) {
	value, ok := row[name]
	if !ok {
		return 0, fmt.Errorf("%w: missing field %q", ErrValidation, name)
	}
	switch v := value.(type) {
	case int:
		if v < 0 {
			return 0, fmt.Errorf("%w: field %q is negative", ErrValidation, name)
		}
		return uint64(v), nil
	case int8:
		if v < 0 {
			return 0, fmt.Errorf("%w: field %q is negative", ErrValidation, name)
		}
		return uint64(v), nil
	case int16:
		if v < 0 {
			return 0, fmt.Errorf("%w: field %q is negative", ErrValidation, name)
		}
		return uint64(v), nil
	case int32:
		if v < 0 {
			return 0, fmt.Errorf("%w: field %q is negative", ErrValidation, name)
		}
		return uint64(v), nil
	case int64:
		if v < 0 {
			return 0, fmt.Errorf("%w: field %q is negative", ErrValidation, name)
		}
		return uint64(v), nil
	case uint:
		return uint64(v), nil
	case uint8:
		return uint64(v), nil
	case uint16:
		return uint64(v), nil
	case uint32:
		return uint64(v), nil
	case uint64:
		return v, nil
	default:
		return 0, fmt.Errorf("%w: field %q is %T, want uint64", ErrValidation, name, value)
	}
}

func rowUint8(row map[string]any, name string) (uint8, error) {
	value, err := rowUint64(row, name)
	if err != nil {
		return 0, err
	}
	if value > 255 {
		return 0, fmt.Errorf("%w: field %q overflows uint8", ErrValidation, name)
	}
	return uint8(value), nil
}
