package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"

	"github.com/WuKongIM/WuKongIM/pkg/db/inspect"
	msgdb "github.com/WuKongIM/WuKongIM/pkg/db/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultVerifyPageSize      = 1000
	defaultVerifyMaxMismatches = 20
)

// VerifyMode controls how much row content contributes to store comparison.
type VerifyMode string

const (
	// VerifyModeSummary compares stable row identities and payload checksums.
	VerifyModeSummary VerifyMode = "summary"
	// VerifyModeFull compares all supported bundle-v1 row content.
	VerifyModeFull VerifyMode = "full"
)

// VerifyOptions controls how two read-only WKDB stores are compared.
type VerifyOptions struct {
	// HashSlotCount bounds metadata hash-slot scans and must be nonzero.
	HashSlotCount uint16
	// PageSize bounds rows read from inspect APIs per scan page.
	PageSize int
	// Mode controls whether message payload bytes are included in the digest.
	Mode VerifyMode
	// MaxMismatches bounds mismatch details retained in the report.
	MaxMismatches int
}

// VerifyDatasetReport summarizes one logical data set comparison.
type VerifyDatasetReport struct {
	Name         string `json:"name"`
	Equal        bool   `json:"equal"`
	SourceRows   int64  `json:"source_rows"`
	TargetRows   int64  `json:"target_rows"`
	SourceDigest string `json:"source_digest"`
	TargetDigest string `json:"target_digest"`
}

// VerifyMismatch describes one bounded comparison mismatch.
type VerifyMismatch struct {
	Scope  string `json:"scope"`
	Detail string `json:"detail"`
}

// VerifyReport summarizes equality for bundle-v1 WKDB datasets.
type VerifyReport struct {
	Equal         bool                  `json:"equal"`
	Mode          VerifyMode            `json:"mode"`
	HashSlotCount uint16                `json:"hash_slot_count"`
	Meta          []VerifyDatasetReport `json:"meta"`
	Message       []VerifyDatasetReport `json:"message"`
	Mismatches    []VerifyMismatch      `json:"mismatches,omitempty"`
}

type verifyMetaSpec struct {
	name  string
	table string
}

var verifyMetaSpecs = []verifyMetaSpec{
	{name: "meta.users", table: "user"},
	{name: "meta.devices", table: "device"},
	{name: "meta.channels", table: "channel"},
	{name: "meta.subscribers", table: "subscriber"},
	{name: "meta.user_channel_memberships", table: "user_channel_membership"},
	{name: "meta.conversations", table: "conversation"},
	{name: "meta.channel_latest", table: "channel_latest"},
}

// VerifyStores compares supported bundle-v1 datasets from two read-only WKDB stores.
func VerifyStores(ctx context.Context, source, target *inspect.Store, opts VerifyOptions) (VerifyReport, error) {
	opts, err := normalizeVerifyOptions(opts)
	if err != nil {
		return VerifyReport{}, err
	}
	report := VerifyReport{
		Equal:         true,
		Mode:          opts.Mode,
		HashSlotCount: opts.HashSlotCount,
		Meta:          make([]VerifyDatasetReport, 0, len(verifyMetaSpecs)),
		Message:       make([]VerifyDatasetReport, 0, 2),
	}
	if source == nil || target == nil {
		return report, fmt.Errorf("%w: verify source and target stores are required", ErrValidation)
	}
	if source.Meta() == nil || target.Meta() == nil {
		return report, fmt.Errorf("%w: verify metadata stores are required", ErrValidation)
	}
	if source.Messages() == nil || target.Messages() == nil {
		return report, fmt.Errorf("%w: verify message stores are required", ErrValidation)
	}

	for _, spec := range verifyMetaSpecs {
		dataset, mismatches, err := verifyMetaTable(ctx, source.Meta(), target.Meta(), opts, spec)
		if err != nil {
			return report, err
		}
		report.Meta = append(report.Meta, dataset)
		appendVerifyMismatches(&report, opts.MaxMismatches, mismatches...)
	}
	messageCatalog, mismatches, err := verifyMessageCatalog(ctx, source.Messages(), target.Messages(), opts)
	if err != nil {
		return report, err
	}
	report.Message = append(report.Message, messageCatalog)
	appendVerifyMismatches(&report, opts.MaxMismatches, mismatches...)

	messages, mismatches, err := verifyMessages(ctx, source.Messages(), target.Messages(), opts)
	if err != nil {
		return report, err
	}
	report.Message = append(report.Message, messages)
	appendVerifyMismatches(&report, opts.MaxMismatches, mismatches...)

	report.Equal = len(report.Mismatches) == 0
	return report, nil
}

func normalizeVerifyOptions(opts VerifyOptions) (VerifyOptions, error) {
	if opts.Mode == "" {
		opts.Mode = VerifyModeSummary
	}
	if opts.PageSize <= 0 {
		opts.PageSize = defaultVerifyPageSize
	}
	if opts.MaxMismatches <= 0 {
		opts.MaxMismatches = defaultVerifyMaxMismatches
	}
	if opts.HashSlotCount == 0 {
		return opts, fmt.Errorf("%w: verify hash slot count is required", ErrValidation)
	}
	switch opts.Mode {
	case VerifyModeSummary, VerifyModeFull:
		return opts, nil
	default:
		return opts, fmt.Errorf("%w: unknown verify mode %q", ErrValidation, opts.Mode)
	}
}

func verifyMetaTable(ctx context.Context, source, target *metadb.MetaDB, opts VerifyOptions, spec verifyMetaSpec) (VerifyDatasetReport, []VerifyMismatch, error) {
	sourceRows, sourceDigest, err := scanMetaDigest(ctx, source, opts, spec.table)
	if err != nil {
		return VerifyDatasetReport{}, nil, fmt.Errorf("verify %s source: %w", spec.name, err)
	}
	targetRows, targetDigest, err := scanMetaDigest(ctx, target, opts, spec.table)
	if err != nil {
		return VerifyDatasetReport{}, nil, fmt.Errorf("verify %s target: %w", spec.name, err)
	}
	return newVerifyDatasetReport(spec.name, sourceRows, targetRows, sourceDigest, targetDigest), verifyDatasetMismatches(spec.name, sourceRows, targetRows, sourceDigest, targetDigest), nil
}

type digestMetaRow struct {
	HashSlot uint16            `json:"hash_slot"`
	Row      metadb.InspectRow `json:"row"`
}

func scanMetaDigest(ctx context.Context, db *metadb.MetaDB, opts VerifyOptions, table string) (int64, string, error) {
	digest := newVerifyDigest()
	var rows int64
	for slot := uint16(0); slot < opts.HashSlotCount; slot++ {
		var after *metadb.InspectCursor
		for {
			result, err := metadb.InspectScan(ctx, db, metadb.InspectScanRequest{
				Table:       table,
				HashSlot:    metadb.HashSlot(slot),
				HashSlotSet: true,
				After:       after,
				Limit:       opts.PageSize,
			})
			if err != nil {
				return 0, "", fmt.Errorf("slot %d: %w", slot, err)
			}
			for _, row := range result.Rows {
				if err := digest.write(digestMetaRow{HashSlot: slot, Row: row}); err != nil {
					return 0, "", err
				}
				rows++
			}
			if result.Done {
				break
			}
			if result.Next == nil {
				return 0, "", fmt.Errorf("%w: verify %s slot %d missing inspect cursor", ErrValidation, table, slot)
			}
			after = result.Next
		}
	}
	return rows, digest.sum(), nil
}

func verifyMessageCatalog(ctx context.Context, source, target *msgdb.MessageDB, opts VerifyOptions) (VerifyDatasetReport, []VerifyMismatch, error) {
	const name = "message.channels"
	sourceRows, sourceDigest, err := scanMessageCatalogDigest(ctx, source, opts)
	if err != nil {
		return VerifyDatasetReport{}, nil, fmt.Errorf("verify %s source: %w", name, err)
	}
	targetRows, targetDigest, err := scanMessageCatalogDigest(ctx, target, opts)
	if err != nil {
		return VerifyDatasetReport{}, nil, fmt.Errorf("verify %s target: %w", name, err)
	}
	return newVerifyDatasetReport(name, sourceRows, targetRows, sourceDigest, targetDigest), verifyDatasetMismatches(name, sourceRows, targetRows, sourceDigest, targetDigest), nil
}

func scanMessageCatalogDigest(ctx context.Context, db *msgdb.MessageDB, opts VerifyOptions) (int64, string, error) {
	digest := newVerifyDigest()
	var rows int64
	var afterChannelKey string
	for {
		result, err := msgdb.InspectChannels(ctx, db, msgdb.InspectMessageRequest{
			AfterChannelKey: afterChannelKey,
			Limit:           opts.PageSize,
		})
		if err != nil {
			return 0, "", err
		}
		for _, row := range result.Rows {
			if err := digest.write(row); err != nil {
				return 0, "", err
			}
			rows++
		}
		if result.Done {
			break
		}
		if result.Next == nil || result.Next.AfterChannelKey == "" {
			return 0, "", fmt.Errorf("%w: verify message channels missing inspect cursor", ErrValidation)
		}
		afterChannelKey = result.Next.AfterChannelKey
	}
	return rows, digest.sum(), nil
}

func verifyMessages(ctx context.Context, source, target *msgdb.MessageDB, opts VerifyOptions) (VerifyDatasetReport, []VerifyMismatch, error) {
	const name = "message.messages"
	sourceRows, sourceDigest, err := scanMessagesDigest(ctx, source, opts)
	if err != nil {
		return VerifyDatasetReport{}, nil, fmt.Errorf("verify %s source: %w", name, err)
	}
	targetRows, targetDigest, err := scanMessagesDigest(ctx, target, opts)
	if err != nil {
		return VerifyDatasetReport{}, nil, fmt.Errorf("verify %s target: %w", name, err)
	}
	return newVerifyDatasetReport(name, sourceRows, targetRows, sourceDigest, targetDigest), verifyDatasetMismatches(name, sourceRows, targetRows, sourceDigest, targetDigest), nil
}

func scanMessagesDigest(ctx context.Context, db *msgdb.MessageDB, opts VerifyOptions) (int64, string, error) {
	digest := newVerifyDigest()
	var rows int64
	var afterChannelKey string
	for {
		channels, err := msgdb.InspectChannels(ctx, db, msgdb.InspectMessageRequest{
			AfterChannelKey: afterChannelKey,
			Limit:           opts.PageSize,
		})
		if err != nil {
			return 0, "", err
		}
		for _, channel := range channels.Rows {
			channelKey, err := rowString(channel, "channel_key")
			if err != nil {
				return 0, "", err
			}
			channelRows, err := digestChannelMessages(ctx, db, opts, channelKey, digest)
			if err != nil {
				return 0, "", err
			}
			rows += channelRows
		}
		if channels.Done {
			break
		}
		if channels.Next == nil || channels.Next.AfterChannelKey == "" {
			return 0, "", fmt.Errorf("%w: verify message channels missing inspect cursor", ErrValidation)
		}
		afterChannelKey = channels.Next.AfterChannelKey
	}
	return rows, digest.sum(), nil
}

type digestMessageSummaryRow struct {
	ChannelKey        string `json:"channel_key"`
	MessageSeq        uint64 `json:"message_seq"`
	MessageID         uint64 `json:"message_id"`
	ClientMsgNo       string `json:"client_msg_no"`
	FromUID           string `json:"from_uid"`
	ServerTimestampMS int64  `json:"server_timestamp_ms"`
	PayloadHash       uint64 `json:"payload_hash"`
	PayloadSize       uint64 `json:"payload_size"`
}

type digestMessageFullRow struct {
	ChannelKey        string `json:"channel_key"`
	MessageSeq        uint64 `json:"message_seq"`
	MessageID         uint64 `json:"message_id"`
	ClientMsgNo       string `json:"client_msg_no"`
	FromUID           string `json:"from_uid"`
	ServerTimestampMS int64  `json:"server_timestamp_ms"`
	PayloadHash       uint64 `json:"payload_hash"`
	PayloadSize       uint64 `json:"payload_size"`
	Payload           []byte `json:"payload"`
}

func digestChannelMessages(ctx context.Context, db *msgdb.MessageDB, opts VerifyOptions, channelKey string, digest *verifyDigest) (int64, error) {
	var rows int64
	var afterSeq uint64
	for {
		result, err := msgdb.InspectMessages(ctx, db, msgdb.InspectMessageRequest{
			ChannelKey: channelKey,
			AfterSeq:   afterSeq,
			Limit:      opts.PageSize,
		})
		if err != nil {
			return 0, fmt.Errorf("channel_key=%q: %w", channelKey, err)
		}
		for _, row := range result.Rows {
			if err := digest.writeMessageRow(channelKey, row, opts.Mode); err != nil {
				return 0, fmt.Errorf("channel_key=%q: %w", channelKey, err)
			}
			rows++
		}
		if result.Done {
			break
		}
		if result.Next == nil {
			return 0, fmt.Errorf("%w: verify messages missing inspect cursor channel_key=%q", ErrValidation, channelKey)
		}
		afterSeq = result.Next.AfterSeq
	}
	return rows, nil
}

type verifyDigest struct {
	hash    hash.Hash
	encoder *json.Encoder
}

func newVerifyDigest() *verifyDigest {
	h := sha256.New()
	return &verifyDigest{hash: h, encoder: json.NewEncoder(h)}
}

func (d *verifyDigest) write(value any) error {
	if err := d.encoder.Encode(value); err != nil {
		return fmt.Errorf("verify digest encode: %w", err)
	}
	return nil
}

func (d *verifyDigest) writeMessageRow(channelKey string, row msgdb.InspectMessageRow, mode VerifyMode) error {
	summary, payload, err := digestMessageRow(channelKey, row)
	if err != nil {
		return err
	}
	if mode == VerifyModeFull {
		return d.write(digestMessageFullRow{
			ChannelKey:        summary.ChannelKey,
			MessageSeq:        summary.MessageSeq,
			MessageID:         summary.MessageID,
			ClientMsgNo:       summary.ClientMsgNo,
			FromUID:           summary.FromUID,
			ServerTimestampMS: summary.ServerTimestampMS,
			PayloadHash:       summary.PayloadHash,
			PayloadSize:       summary.PayloadSize,
			Payload:           payload,
		})
	}
	return d.write(summary)
}

func (d *verifyDigest) sum() string {
	return hex.EncodeToString(d.hash.Sum(nil))
}

func digestMessageRow(channelKey string, row msgdb.InspectMessageRow) (digestMessageSummaryRow, []byte, error) {
	messageSeq, err := rowUint64(row, "message_seq")
	if err != nil {
		return digestMessageSummaryRow{}, nil, err
	}
	messageID, err := rowUint64(row, "message_id")
	if err != nil {
		return digestMessageSummaryRow{}, nil, err
	}
	clientMsgNo, err := rowString(row, "client_msg_no")
	if err != nil {
		return digestMessageSummaryRow{}, nil, err
	}
	fromUID, err := rowString(row, "from_uid")
	if err != nil {
		return digestMessageSummaryRow{}, nil, err
	}
	serverTimestampMS, err := rowInt64(row, "server_timestamp_ms")
	if err != nil {
		return digestMessageSummaryRow{}, nil, err
	}
	payloadHash, err := rowUint64(row, "payload_hash")
	if err != nil {
		return digestMessageSummaryRow{}, nil, err
	}
	payloadSize, err := rowUint64(row, "payload_size")
	if err != nil {
		return digestMessageSummaryRow{}, nil, err
	}
	payload, err := rowBytes(row, "payload")
	if err != nil {
		return digestMessageSummaryRow{}, nil, err
	}
	return digestMessageSummaryRow{
		ChannelKey:        channelKey,
		MessageSeq:        messageSeq,
		MessageID:         messageID,
		ClientMsgNo:       clientMsgNo,
		FromUID:           fromUID,
		ServerTimestampMS: serverTimestampMS,
		PayloadHash:       payloadHash,
		PayloadSize:       payloadSize,
	}, payload, nil
}

func newVerifyDatasetReport(name string, sourceRows int64, targetRows int64, sourceDigest string, targetDigest string) VerifyDatasetReport {
	return VerifyDatasetReport{
		Name:         name,
		Equal:        sourceRows == targetRows && sourceDigest == targetDigest,
		SourceRows:   sourceRows,
		TargetRows:   targetRows,
		SourceDigest: sourceDigest,
		TargetDigest: targetDigest,
	}
}

func verifyDatasetMismatches(name string, sourceRows int64, targetRows int64, sourceDigest string, targetDigest string) []VerifyMismatch {
	if sourceRows == targetRows && sourceDigest == targetDigest {
		return nil
	}
	mismatches := make([]VerifyMismatch, 0, 2)
	if sourceRows != targetRows {
		mismatches = append(mismatches, VerifyMismatch{
			Scope:  name,
			Detail: fmt.Sprintf("row count mismatch source=%d target=%d", sourceRows, targetRows),
		})
	}
	if sourceDigest != targetDigest {
		mismatches = append(mismatches, VerifyMismatch{
			Scope:  name,
			Detail: fmt.Sprintf("digest mismatch source=%s target=%s", sourceDigest, targetDigest),
		})
	}
	return mismatches
}

func appendVerifyMismatches(report *VerifyReport, max int, mismatches ...VerifyMismatch) {
	for _, mismatch := range mismatches {
		if len(report.Mismatches) >= max {
			return
		}
		report.Mismatches = append(report.Mismatches, mismatch)
	}
}
