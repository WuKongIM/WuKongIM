package transfer

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/db"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultSubscriberBatchSize = 1024
	defaultMessageBatchSize    = 1024
	defaultMessageBatchBytes   = 4 << 20
)

// ImportBundle validates a WKDB import bundle and writes it through typed NodeStore APIs.
func ImportBundle(ctx context.Context, root string, store *db.NodeStore, opts ImportOptions) (ImportStats, error) {
	if store == nil {
		return ImportStats{}, fmt.Errorf("import bundle: nil target store")
	}
	opts = normalizeImportOptions(opts)
	stats, err := ValidateBundle(ctx, root, opts)
	if err != nil {
		return stats, err
	}
	manifest, err := LoadManifest(root)
	if err != nil {
		return stats, err
	}
	if _, err := validateHashSlotCount(manifest, opts); err != nil {
		return stats, err
	}
	if opts.RequireEmpty {
		if err := checkTargetEmpty(ctx, store); err != nil {
			return stats, err
		}
	}

	entries := groupManifestEntries(manifest)
	for _, kind := range []FileKind{
		FileKindMetaUsers,
		FileKindMetaDevices,
		FileKindMetaChannels,
		FileKindMetaSubscribers,
		FileKindMetaUserChannelMemberships,
		FileKindMetaConversations,
		FileKindMetaChannelLatest,
	} {
		for _, entry := range entries[kind] {
			if err := importMetaEntry(ctx, root, entry, store, opts, &stats); err != nil {
				return stats, err
			}
		}
	}

	channels := newMessageChannelStream(ctx, root, entries[FileKindMessageChannels], nil)
	defer channels.Close()
	for _, entry := range entries[FileKindMessageMessages] {
		if err := importMessageEntry(ctx, root, entry, store.Messages(), channels, opts, &stats); err != nil {
			return stats, err
		}
	}
	return stats, nil
}

func normalizeImportOptions(opts ImportOptions) ImportOptions {
	if opts.SubscriberBatchSize <= 0 {
		opts.SubscriberBatchSize = defaultSubscriberBatchSize
	}
	if opts.MessageBatchSize <= 0 {
		opts.MessageBatchSize = defaultMessageBatchSize
	}
	if opts.MessageBatchBytes <= 0 {
		opts.MessageBatchBytes = defaultMessageBatchBytes
	}
	return opts
}

func groupManifestEntries(manifest Manifest) map[FileKind][]FileEntry {
	entries := make(map[FileKind][]FileEntry)
	for _, entry := range manifest.Files {
		entries[entry.Kind] = append(entries[entry.Kind], entry)
	}
	return entries
}

func checkTargetEmpty(ctx context.Context, store *db.NodeStore) error {
	meta := store.Meta()
	for _, table := range metadb.InspectTables() {
		nonEmpty, err := metaTableHasAnyRow(ctx, meta, table.Name)
		if err != nil {
			return err
		}
		if nonEmpty {
			return fmt.Errorf("non-empty target: meta table %q", table.Name)
		}
	}
	result, err := msgdb.InspectChannels(ctx, store.Messages(), msgdb.InspectMessageRequest{Limit: 1})
	if err != nil {
		return fmt.Errorf("inspect target message channels: %w", err)
	}
	if len(result.Rows) > 0 {
		return fmt.Errorf("non-empty target: message channels")
	}
	return nil
}

func metaTableHasAnyRow(ctx context.Context, meta *metadb.MetaDB, tableName string) (bool, error) {
	result, err := metadb.InspectScan(ctx, meta, metadb.InspectScanRequest{
		Table:         tableName,
		HashSlotCount: ^uint16(0),
		Limit:         1,
	})
	if err != nil {
		return false, fmt.Errorf("inspect target meta table %q: %w", tableName, err)
	}
	if len(result.Rows) > 0 {
		return true, nil
	}
	result, err = metadb.InspectScan(ctx, meta, metadb.InspectScanRequest{
		Table:       tableName,
		HashSlot:    ^uint16(0),
		HashSlotSet: true,
		Limit:       1,
	})
	if err != nil {
		return false, fmt.Errorf("inspect target meta table %q: %w", tableName, err)
	}
	return len(result.Rows) > 0, nil
}

func importMetaEntry(ctx context.Context, root string, entry FileEntry, store *db.NodeStore, opts ImportOptions, stats *ImportStats) error {
	switch entry.Kind {
	case FileKindMetaSubscribers:
		return importSubscriberEntry(ctx, root, entry, store.Meta(), opts, stats)
	default:
		return importBundleEntry(ctx, root, entry, func(record any) error {
			if err := importMetaRecord(ctx, store.Meta(), entry.Kind, record); err != nil {
				return err
			}
			stats.RowsWritten++
			if entry.Kind == FileKindMetaChannels {
				stats.ChannelsImported++
			}
			return nil
		})
	}
}

func importMetaRecord(ctx context.Context, meta *metadb.MetaDB, kind FileKind, record any) error {
	switch kind {
	case FileKindMetaUsers:
		row := record.(UserRecord)
		return meta.HashSlot(row.HashSlot).UpsertUser(ctx, metadb.User{
			UID:         row.UID,
			Token:       row.Token,
			DeviceFlag:  row.DeviceFlag,
			DeviceLevel: row.DeviceLevel,
		})
	case FileKindMetaDevices:
		row := record.(DeviceRecord)
		return meta.HashSlot(row.HashSlot).UpsertDevice(ctx, metadb.Device{
			UID:         row.UID,
			DeviceFlag:  row.DeviceFlag,
			Token:       row.Token,
			DeviceLevel: row.DeviceLevel,
		})
	case FileKindMetaChannels:
		row := record.(ChannelRecord)
		return meta.HashSlot(row.HashSlot).UpsertChannel(ctx, metadb.Channel{
			ChannelID:                 row.ChannelID,
			ChannelType:               row.ChannelType,
			Ban:                       row.Ban,
			Disband:                   row.Disband,
			SendBan:                   row.SendBan,
			AllowStranger:             row.AllowStranger,
			Large:                     row.Large,
			SubscriberMutationVersion: uint64(row.SubscriberMutationVersion),
		})
	case FileKindMetaUserChannelMemberships:
		row := record.(UserChannelMembershipRecord)
		return meta.HashSlot(row.HashSlot).UpsertUserChannelMembership(ctx, metadb.UserChannelMembership{
			UID:         row.UID,
			ChannelID:   row.ChannelID,
			ChannelType: row.ChannelType,
			JoinSeq:     uint64(row.JoinSeq),
			UpdatedAt:   row.UpdatedAtMS,
		})
	case FileKindMetaConversations:
		row := record.(ConversationRecord)
		kind, err := conversationKind(row.Kind)
		if err != nil {
			return err
		}
		return meta.HashSlot(row.HashSlot).UpsertConversationState(ctx, metadb.ConversationState{
			UID:          row.UID,
			Kind:         kind,
			ChannelID:    row.ChannelID,
			ChannelType:  row.ChannelType,
			ReadSeq:      uint64(row.ReadSeq),
			DeletedToSeq: uint64(row.DeletedToSeq),
			ActiveAt:     row.ActiveAt,
			UpdatedAt:    row.UpdatedAt,
			SparseActive: row.SparseActive,
		})
	case FileKindMetaChannelLatest:
		row := record.(ChannelLatestRecord)
		return meta.HashSlot(row.HashSlot).UpsertChannelLatest(ctx, metadb.ChannelLatest{
			ChannelID:      row.ChannelID,
			ChannelType:    row.ChannelType,
			LastMessageID:  uint64(row.LastMessageID),
			LastMessageSeq: uint64(row.LastMessageSeq),
			LastAt:         row.LastAt,
			FromUID:        row.FromUID,
			ClientMsgNo:    row.ClientMsgNo,
			Payload:        row.Payload,
			UpdatedAt:      row.UpdatedAt,
		})
	default:
		return fmt.Errorf("%w: unsupported import kind %q", ErrValidation, kind)
	}
}

func conversationKind(kind string) (metadb.ConversationKind, error) {
	switch kind {
	case "normal":
		return metadb.ConversationKindNormal, nil
	case "cmd":
		return metadb.ConversationKindCMD, nil
	default:
		return 0, fmt.Errorf("%w: unknown conversation kind %q", ErrValidation, kind)
	}
}

type subscriberGroup struct {
	hashSlot    uint16
	channelID   string
	channelType int64
}

func importSubscriberEntry(ctx context.Context, root string, entry FileEntry, meta *metadb.MetaDB, opts ImportOptions, stats *ImportStats) error {
	var current subscriberGroup
	var haveCurrent bool
	uids := make([]string, 0, opts.SubscriberBatchSize)

	flush := func() error {
		if !haveCurrent || len(uids) == 0 {
			return nil
		}
		if err := meta.HashSlot(current.hashSlot).AddSubscribers(ctx, current.channelID, current.channelType, uids, 0); err != nil {
			return err
		}
		uids = uids[:0]
		return nil
	}

	err := importBundleEntry(ctx, root, entry, func(record any) error {
		row := record.(SubscriberRecord)
		next := subscriberGroup{hashSlot: row.HashSlot, channelID: row.ChannelID, channelType: row.ChannelType}
		if haveCurrent && current != next {
			if err := flush(); err != nil {
				return err
			}
		}
		current = next
		haveCurrent = true
		uids = append(uids, row.UID)
		stats.RowsWritten++
		stats.SubscribersImported++
		if len(uids) >= opts.SubscriberBatchSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

func importMessageEntry(ctx context.Context, root string, entry FileEntry, messages *msgdb.MessageDB, channels *messageChannelStream, opts ImportOptions, stats *ImportStats) error {
	var currentKey string
	var currentID msgdb.ChannelID
	var haveCurrent bool
	records := make([]msgdb.Record, 0, opts.MessageBatchSize)
	var batchBaseSeq uint64
	var recordBytes int

	flush := func() error {
		if !haveCurrent || len(records) == 0 {
			return nil
		}
		log := messages.Channel(msgdb.ChannelKey(currentKey), currentID)
		_, err := log.Append(ctx, records, msgdb.AppendOptions{
			Mode:    msgdb.AppendStrict,
			BaseSeq: batchBaseSeq,
		})
		if err != nil {
			return err
		}
		records = records[:0]
		batchBaseSeq = 0
		recordBytes = 0
		return nil
	}

	err := importBundleEntry(ctx, root, entry, func(record any) error {
		row := record.(MessageRecord)
		if !haveCurrent || currentKey != row.ChannelKey {
			if err := flush(); err != nil {
				return err
			}
			channelRecord, ok, err := channels.Lookup(row.ChannelKey)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: missing message channel channel_key=%q", ErrValidation, row.ChannelKey)
			}
			currentKey = row.ChannelKey
			currentID = msgdb.ChannelID{ID: channelRecord.ChannelID, Type: channelRecord.ChannelType}
			haveCurrent = true
		}
		if len(records) == 0 {
			batchBaseSeq = uint64(row.MessageSeq)
		}
		records = append(records, msgdb.Record{
			ID:                uint64(row.MessageID),
			ClientMsgNo:       row.ClientMsgNo,
			FromUID:           row.FromUID,
			Payload:           row.Payload,
			SizeBytes:         len(row.Payload),
			ServerTimestampMS: row.ServerTimestampMS,
		})
		recordBytes += len(row.Payload)
		stats.RowsWritten++
		stats.MessagesImported++
		if len(records) >= opts.MessageBatchSize || recordBytes >= opts.MessageBatchBytes {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

func importBundleEntry(ctx context.Context, root string, entry FileEntry, visit func(any) error) error {
	file, _, err := openBundleFile(root, entry)
	if err != nil {
		return err
	}

	var fileRows int64
	err = readJSONL(ctx, file, entry.Kind, func(record any) error {
		if err := visit(record); err != nil {
			return err
		}
		fileRows++
		return nil
	})
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("import %q: %w", entry.Path, err)
	}
	if closeErr != nil {
		return fmt.Errorf("%w: close %q: %v", ErrValidation, entry.Path, closeErr)
	}
	if err := validateFileRowCount(entry, fileRows); err != nil {
		return err
	}
	return nil
}
