package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// ErasureLedgerEventFormat identifies encrypted permanent-erasure payloads.
	ErasureLedgerEventFormat = "wukongim-erasure-ledger-event"
	// ErasureLedgerEventVersion is the current encrypted event payload version.
	ErasureLedgerEventVersion uint32 = 1
	// ErasureLedgerRecordFormat identifies signed encrypted-event references.
	ErasureLedgerRecordFormat = "wukongim-erasure-ledger-record"
	// ErasureLedgerRecordVersion is the current signed event-reference version.
	ErasureLedgerRecordVersion uint32 = 1
	// ErasureLedgerCommitFormat identifies signed monotonic ledger commits.
	ErasureLedgerCommitFormat = "wukongim-erasure-ledger-commit"
	// ErasureLedgerCommitVersion is the current signed commit version.
	ErasureLedgerCommitVersion uint32 = 1
	// ErasureLedgerSnapshotVersion identifies a restore plan's exact ledger prefix digest.
	ErasureLedgerSnapshotVersion uint32 = 1
	// EmptyErasureLedgerSnapshotSHA256 is the SHA-256 digest of the empty ledger prefix.
	EmptyErasureLedgerSnapshotSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	maxErasureLedgerArtifactBytes = 64 << 10
)

// ErasureLedgerEvent is the encrypted business identity and permanent message
// prefix erasure requested against one source-cluster generation.
type ErasureLedgerEvent struct {
	// Format identifies the encrypted event schema family.
	Format string `json:"format"`
	// Version selects the encrypted event schema version.
	Version uint32 `json:"version"`
	// RepositoryID identifies the logical dual-repository backup.
	RepositoryID string `json:"repository_id"`
	// SourceClusterID identifies the cluster whose data was erased.
	SourceClusterID string `json:"source_cluster_id"`
	// SourceGeneration fences the erased source-cluster incarnation.
	SourceGeneration string `json:"source_generation"`
	// EventID is the deterministic digest of the erasure identity and boundary.
	EventID string `json:"event_id"`
	// HashSlot identifies the logical metadata and message partition.
	HashSlot uint16 `json:"hash_slot"`
	// ChannelID identifies the erased durable message log.
	ChannelID string `json:"channel_id"`
	// ChannelType identifies the erased channel namespace.
	ChannelType uint8 `json:"channel_type"`
	// ThroughSeq is the inclusive highest message sequence permanently erased.
	ThroughSeq uint64 `json:"through_seq"`
	// RequestedAtUnixMillis records the accepted operator request time in UTC.
	RequestedAtUnixMillis int64 `json:"requested_at_ms"`
}

// ErasureLedgerRecord is a signed reference to one encrypted erasure event.
// It exposes no plaintext Channel identity in repository metadata.
type ErasureLedgerRecord struct {
	// Format identifies the signed event-reference schema family.
	Format string `json:"format"`
	// Version selects the signed event-reference schema version.
	Version uint32 `json:"version"`
	// RepositoryID identifies the logical dual-repository backup.
	RepositoryID string `json:"repository_id"`
	// SourceClusterID identifies the cluster whose event is referenced.
	SourceClusterID string `json:"source_cluster_id"`
	// SourceGeneration fences the source-cluster incarnation.
	SourceGeneration string `json:"source_generation"`
	// EventID identifies the encrypted erasure event without exposing Channel identity.
	EventID string `json:"event_id"`
	// HashSlot identifies the logical partition affected by the event.
	HashSlot uint16 `json:"hash_slot"`
	// CreatedAtUnixMillis records signed record creation time in UTC.
	CreatedAtUnixMillis int64 `json:"created_at_ms"`
	// Object authenticates the encrypted permanent-erasure payload.
	Object ObjectEntry `json:"object"`
	// Signature authenticates every preceding record field.
	Signature *ManifestSignature `json:"signature,omitempty"`
}

// ErasureLedgerCommit is the signed, monotonically sequenced publication
// marker that makes one immutable erasure record part of the replay ledger.
type ErasureLedgerCommit struct {
	// Format identifies the signed ledger-commit schema family.
	Format string `json:"format"`
	// Version selects the signed ledger-commit schema version.
	Version uint32 `json:"version"`
	// RepositoryID identifies the logical dual-repository backup.
	RepositoryID string `json:"repository_id"`
	// SourceClusterID identifies the source cluster whose ledger is committed.
	SourceClusterID string `json:"source_cluster_id"`
	// SourceGeneration fences the source-cluster incarnation.
	SourceGeneration string `json:"source_generation"`
	// Sequence is the contiguous one-based ledger commit position.
	Sequence uint64 `json:"sequence"`
	// EventID identifies the committed encrypted erasure event.
	EventID string `json:"event_id"`
	// RecordKey is the immutable signed event-reference key.
	RecordKey string `json:"record_key"`
	// RecordSHA256 authenticates the exact signed event-reference bytes.
	RecordSHA256 string `json:"record_sha256"`
	// CreatedAtUnixMillis records commit publication time in UTC.
	CreatedAtUnixMillis int64 `json:"created_at_ms"`
	// PrimaryRepository identifies the first verified failure-domain copy.
	PrimaryRepository string `json:"primary_repository"`
	// SecondaryRepository identifies the second verified failure-domain copy.
	SecondaryRepository string `json:"secondary_repository"`
	// Signature authenticates every preceding commit field.
	Signature *ManifestSignature `json:"signature,omitempty"`
}

// ComputeErasureEventID returns the deterministic idempotency identity for one
// permanent Channel message-prefix erasure.
func ComputeErasureEventID(repositoryID, sourceClusterID, sourceGeneration, channelID string, channelType uint8, throughSeq uint64) string {
	identity := struct {
		RepositoryID     string `json:"repository_id"`
		SourceClusterID  string `json:"source_cluster_id"`
		SourceGeneration string `json:"source_generation"`
		ChannelID        string `json:"channel_id"`
		ChannelType      uint8  `json:"channel_type"`
		ThroughSeq       uint64 `json:"through_seq"`
	}{
		RepositoryID: strings.TrimSpace(repositoryID), SourceClusterID: strings.TrimSpace(sourceClusterID),
		SourceGeneration: strings.TrimSpace(sourceGeneration), ChannelID: strings.TrimSpace(channelID),
		ChannelType: channelType, ThroughSeq: throughSeq,
	}
	body, _ := json.Marshal(identity)
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

// ErasureLedgerRecordKey returns the immutable repository key for one event record.
func ErasureLedgerRecordKey(hashSlot uint16, eventID string) string {
	return fmt.Sprintf("erasure-ledger/events/%04x/%s.json", hashSlot, eventID)
}

// ErasureLedgerReceiptKey returns the deterministic committed-event receipt key.
// The receipt contains the same signed bytes as the event's sequence commit and
// allows constant-time idempotency checks after later events have committed.
func ErasureLedgerReceiptKey(eventID string) string {
	return "erasure-ledger/receipts/" + eventID + ".json"
}

// ErasureLedgerCommitKey returns the lexicographically ordered immutable key for one commit.
func ErasureLedgerCommitKey(sequence uint64) string {
	return fmt.Sprintf("erasure-ledger/commits/%020d.json", sequence)
}

// ValidateErasureLedgerRecordKey validates the authoritative record-key grammar
// and its binding to a deterministic event identity.
func ValidateErasureLedgerRecordKey(key, eventID string) error {
	if err := validateLowerSHA256(eventID); err != nil {
		return fmt.Errorf("%w: erasure ledger event id is invalid", ErrInvalidManifest)
	}
	return validateErasureRecordKey(key, eventID)
}

// MarshalErasureLedgerEvent strictly validates and encodes one encrypted event payload.
func MarshalErasureLedgerEvent(event ErasureLedgerEvent) ([]byte, error) {
	if err := validateErasureLedgerEvent(event); err != nil {
		return nil, err
	}
	body, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal erasure ledger event: %w", err)
	}
	if len(body) > maxErasureLedgerArtifactBytes {
		return nil, fmt.Errorf("%w: erasure ledger event exceeds size limit", ErrInvalidManifest)
	}
	return body, nil
}

// LoadErasureLedgerEvent strictly decodes one authenticated decrypted event payload.
func LoadErasureLedgerEvent(body []byte) (ErasureLedgerEvent, error) {
	if len(body) == 0 || len(body) > maxErasureLedgerArtifactBytes {
		return ErasureLedgerEvent{}, fmt.Errorf("%w: erasure ledger event size is invalid", ErrInvalidManifest)
	}
	var event ErasureLedgerEvent
	if err := decodeStrictErasureJSON(body, &event); err != nil {
		return ErasureLedgerEvent{}, fmt.Errorf("%w: decode erasure ledger event: %v", ErrInvalidManifest, err)
	}
	if err := validateErasureLedgerEvent(event); err != nil {
		return ErasureLedgerEvent{}, err
	}
	return event, nil
}

// SignErasureLedgerRecord signs the canonical encrypted-event reference.
func SignErasureLedgerRecord(ctx context.Context, record ErasureLedgerRecord, signer ManifestSigner, signingKeyID string) (ErasureLedgerRecord, error) {
	if signer == nil || strings.TrimSpace(signingKeyID) == "" {
		return ErasureLedgerRecord{}, fmt.Errorf("%w: erasure ledger signer is required", ErrInvalidSignature)
	}
	record.Signature = nil
	canonical, err := canonicalErasureLedgerRecord(record)
	if err != nil {
		return ErasureLedgerRecord{}, err
	}
	signature, err := signer.Sign(ctx, signingKeyID, canonical)
	if err != nil {
		return ErasureLedgerRecord{}, fmt.Errorf("%w: sign erasure ledger record: %v", ErrInvalidSignature, err)
	}
	if signature.KeyID != signingKeyID || strings.TrimSpace(signature.Algorithm) == "" || len(signature.Value) == 0 {
		return ErasureLedgerRecord{}, fmt.Errorf("%w: erasure ledger record signer metadata mismatch", ErrInvalidSignature)
	}
	record.Signature = &signature
	return record, nil
}

// MarshalErasureLedgerRecord validates and encodes one signed event reference.
func MarshalErasureLedgerRecord(record ErasureLedgerRecord) ([]byte, error) {
	if err := validateErasureLedgerRecord(record, true); err != nil {
		return nil, err
	}
	body, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("marshal erasure ledger record: %w", err)
	}
	if len(body) > maxErasureLedgerArtifactBytes {
		return nil, fmt.Errorf("%w: erasure ledger record exceeds size limit", ErrInvalidManifest)
	}
	return body, nil
}

// LoadErasureLedgerRecord strictly decodes and verifies one signed event reference.
func LoadErasureLedgerRecord(ctx context.Context, body []byte, signer ManifestSigner) (ErasureLedgerRecord, error) {
	if signer == nil || len(body) == 0 || len(body) > maxErasureLedgerArtifactBytes {
		return ErasureLedgerRecord{}, fmt.Errorf("%w: erasure ledger record input is invalid", ErrInvalidManifest)
	}
	var record ErasureLedgerRecord
	if err := decodeStrictErasureJSON(body, &record); err != nil {
		return ErasureLedgerRecord{}, fmt.Errorf("%w: decode erasure ledger record: %v", ErrInvalidManifest, err)
	}
	if err := validateErasureLedgerRecord(record, true); err != nil {
		return ErasureLedgerRecord{}, err
	}
	signature := *record.Signature
	canonical, err := canonicalErasureLedgerRecord(record)
	if err != nil {
		return ErasureLedgerRecord{}, err
	}
	if err := signer.Verify(ctx, signature, canonical); err != nil {
		return ErasureLedgerRecord{}, fmt.Errorf("%w: verify erasure ledger record: %v", ErrInvalidSignature, err)
	}
	return record, nil
}

// SignErasureLedgerCommit signs one canonical monotonic ledger commit.
func SignErasureLedgerCommit(ctx context.Context, commit ErasureLedgerCommit, signer ManifestSigner, signingKeyID string) (ErasureLedgerCommit, error) {
	if signer == nil || strings.TrimSpace(signingKeyID) == "" {
		return ErasureLedgerCommit{}, fmt.Errorf("%w: erasure ledger signer is required", ErrInvalidSignature)
	}
	commit.Signature = nil
	canonical, err := canonicalErasureLedgerCommit(commit)
	if err != nil {
		return ErasureLedgerCommit{}, err
	}
	signature, err := signer.Sign(ctx, signingKeyID, canonical)
	if err != nil {
		return ErasureLedgerCommit{}, fmt.Errorf("%w: sign erasure ledger commit: %v", ErrInvalidSignature, err)
	}
	if signature.KeyID != signingKeyID || strings.TrimSpace(signature.Algorithm) == "" || len(signature.Value) == 0 {
		return ErasureLedgerCommit{}, fmt.Errorf("%w: erasure ledger commit signer metadata mismatch", ErrInvalidSignature)
	}
	commit.Signature = &signature
	return commit, nil
}

// MarshalErasureLedgerCommit validates and encodes one signed ledger commit.
func MarshalErasureLedgerCommit(commit ErasureLedgerCommit) ([]byte, error) {
	if err := validateErasureLedgerCommit(commit, true); err != nil {
		return nil, err
	}
	body, err := json.Marshal(commit)
	if err != nil {
		return nil, fmt.Errorf("marshal erasure ledger commit: %w", err)
	}
	if len(body) > maxErasureLedgerArtifactBytes {
		return nil, fmt.Errorf("%w: erasure ledger commit exceeds size limit", ErrInvalidManifest)
	}
	return body, nil
}

// LoadErasureLedgerCommit strictly decodes and verifies one signed ledger commit.
func LoadErasureLedgerCommit(ctx context.Context, body []byte, signer ManifestSigner) (ErasureLedgerCommit, error) {
	if signer == nil || len(body) == 0 || len(body) > maxErasureLedgerArtifactBytes {
		return ErasureLedgerCommit{}, fmt.Errorf("%w: erasure ledger commit input is invalid", ErrInvalidManifest)
	}
	var commit ErasureLedgerCommit
	if err := decodeStrictErasureJSON(body, &commit); err != nil {
		return ErasureLedgerCommit{}, fmt.Errorf("%w: decode erasure ledger commit: %v", ErrInvalidManifest, err)
	}
	if err := validateErasureLedgerCommit(commit, true); err != nil {
		return ErasureLedgerCommit{}, err
	}
	signature := *commit.Signature
	canonical, err := canonicalErasureLedgerCommit(commit)
	if err != nil {
		return ErasureLedgerCommit{}, err
	}
	if err := signer.Verify(ctx, signature, canonical); err != nil {
		return ErasureLedgerCommit{}, fmt.Errorf("%w: verify erasure ledger commit: %v", ErrInvalidSignature, err)
	}
	return commit, nil
}

func canonicalErasureLedgerRecord(record ErasureLedgerRecord) ([]byte, error) {
	record.Signature = nil
	if err := validateErasureLedgerRecord(record, false); err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

func canonicalErasureLedgerCommit(commit ErasureLedgerCommit) ([]byte, error) {
	commit.Signature = nil
	if err := validateErasureLedgerCommit(commit, false); err != nil {
		return nil, err
	}
	return json.Marshal(commit)
}

func validateErasureLedgerEvent(event ErasureLedgerEvent) error {
	if event.Format != ErasureLedgerEventFormat || event.Version != ErasureLedgerEventVersion {
		return fmt.Errorf("%w: erasure ledger event format or version is unsupported", ErrInvalidManifest)
	}
	if !validErasureIdentity(event.RepositoryID) || !validErasureIdentity(event.SourceClusterID) ||
		!validErasureIdentity(event.SourceGeneration) || strings.TrimSpace(event.ChannelID) == "" || len(event.ChannelID) > 4096 ||
		event.ChannelType == 0 || event.ThroughSeq == 0 || event.RequestedAtUnixMillis <= 0 {
		return fmt.Errorf("%w: erasure ledger event is incomplete", ErrInvalidManifest)
	}
	expected := ComputeErasureEventID(event.RepositoryID, event.SourceClusterID, event.SourceGeneration, event.ChannelID, event.ChannelType, event.ThroughSeq)
	if event.EventID != expected {
		return fmt.Errorf("%w: erasure ledger event identity mismatch", ErrInvalidManifest)
	}
	return nil
}

func validateErasureLedgerRecord(record ErasureLedgerRecord, requireSignature bool) error {
	if record.Format != ErasureLedgerRecordFormat || record.Version != ErasureLedgerRecordVersion {
		return fmt.Errorf("%w: erasure ledger record format or version is unsupported", ErrInvalidManifest)
	}
	if !validErasureIdentity(record.RepositoryID) || !validErasureIdentity(record.SourceClusterID) ||
		!validErasureIdentity(record.SourceGeneration) || validateLowerSHA256(record.EventID) != nil || record.CreatedAtUnixMillis <= 0 {
		return fmt.Errorf("%w: erasure ledger record identity is invalid", ErrInvalidManifest)
	}
	if err := validateObjectEntry(record.Object, 0); err != nil || record.Object.Kind != ObjectKindErasureLedger ||
		record.Object.HashSlot != record.HashSlot || !strings.HasPrefix(record.Object.Key, "objects/erasure-ledger/"+record.EventID+"/") {
		return fmt.Errorf("%w: erasure ledger record object is invalid", ErrInvalidManifest)
	}
	if requireSignature {
		if !validErasureSignature(record.Signature) {
			return fmt.Errorf("%w: erasure ledger record signature is required", ErrInvalidSignature)
		}
	} else if record.Signature != nil {
		return fmt.Errorf("%w: unsigned erasure ledger record received a signature", ErrInvalidManifest)
	}
	return nil
}

func validateErasureLedgerCommit(commit ErasureLedgerCommit, requireSignature bool) error {
	if commit.Format != ErasureLedgerCommitFormat || commit.Version != ErasureLedgerCommitVersion {
		return fmt.Errorf("%w: erasure ledger commit format or version is unsupported", ErrInvalidManifest)
	}
	if !validErasureIdentity(commit.RepositoryID) || !validErasureIdentity(commit.SourceClusterID) ||
		!validErasureIdentity(commit.SourceGeneration) || commit.Sequence == 0 || validateLowerSHA256(commit.EventID) != nil ||
		validateLowerSHA256(commit.RecordSHA256) != nil || commit.CreatedAtUnixMillis <= 0 ||
		commit.RecordKey == "" || commit.RecordKey != strings.TrimSuffix(commit.RecordKey, " ") {
		return fmt.Errorf("%w: erasure ledger commit identity is invalid", ErrInvalidManifest)
	}
	if err := validateErasureRecordKey(commit.RecordKey, commit.EventID); err != nil {
		return err
	}
	primary, secondary := strings.TrimSpace(commit.PrimaryRepository), strings.TrimSpace(commit.SecondaryRepository)
	if primary == "" || secondary == "" || primary == secondary || len(primary) > 128 || len(secondary) > 128 {
		return fmt.Errorf("%w: erasure ledger commit repositories are invalid", ErrInvalidManifest)
	}
	if requireSignature {
		if !validErasureSignature(commit.Signature) {
			return fmt.Errorf("%w: erasure ledger commit signature is required", ErrInvalidSignature)
		}
	} else if commit.Signature != nil {
		return fmt.Errorf("%w: unsigned erasure ledger commit received a signature", ErrInvalidManifest)
	}
	return nil
}

func validateErasureRecordKey(key, eventID string) error {
	if err := validateRepositoryKey(key); err != nil {
		return fmt.Errorf("%w: erasure ledger record key: %v", ErrInvalidManifest, err)
	}
	parts := strings.Split(key, "/")
	if len(parts) != 4 || parts[0] != "erasure-ledger" || parts[1] != "events" || len(parts[2]) != 4 || parts[3] != eventID+".json" {
		return fmt.Errorf("%w: erasure ledger record key is invalid", ErrInvalidManifest)
	}
	if _, err := strconv.ParseUint(parts[2], 16, 16); err != nil {
		return fmt.Errorf("%w: erasure ledger record hash slot is invalid", ErrInvalidManifest)
	}
	return nil
}

func validErasureIdentity(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 256
}

func validErasureSignature(signature *ManifestSignature) bool {
	return signature != nil && strings.TrimSpace(signature.Algorithm) != "" && strings.TrimSpace(signature.KeyID) != "" && len(signature.Value) > 0
}

func validateLowerSHA256(value string) error {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return errors.New("invalid SHA-256")
	}
	_, err := hex.DecodeString(value)
	return err
}

func decodeStrictErasureJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing data")
	}
	return nil
}
