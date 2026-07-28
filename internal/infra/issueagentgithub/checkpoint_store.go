package issueagentgithub

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	issueagentcontract "github.com/WuKongIM/WuKongIM/internal/contracts/issueagent"
)

const (
	checkpointMarkerPrefix = "<!-- wukongim-issue-agent-checkpoint:v1\n"
	checkpointMarkerSuffix = "\n-->"
	maxCheckpointComment   = 128 << 10
	maxCheckpointSummary   = 4 << 10
)

// ErrNoCheckpoint reports that no App-authored checkpoint marker exists.
var ErrNoCheckpoint = errors.New("no Issue Agent checkpoint")

// PublicKey is one bounded checkpoint verification-key epoch.
type PublicKey struct {
	ID        string            `json:"id"`
	PublicKey ed25519.PublicKey `json:"public_key"`
	NotBefore time.Time         `json:"not_before"`
	NotAfter  time.Time         `json:"not_after"`
}

// KeySet is protected default-branch checkpoint verification configuration.
type KeySet struct {
	SchemaVersion int         `json:"schema_version"`
	Keys          []PublicKey `json:"keys"`
}

// Signer is Publisher-only private signing material.
type Signer struct {
	KeyID      string
	PrivateKey ed25519.PrivateKey
}

// IssueComment is the bounded GitHub comment projection needed for recovery.
type IssueComment struct {
	ID         int64
	Author     string
	AuthorType string
	Body       string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// VerifiedCheckpoint is the latest complete trusted Issue snapshot.
type VerifiedCheckpoint struct {
	CommentID  int64
	Digest     string
	Checkpoint issueagentcontract.Checkpoint
}

// CheckpointStore signs new comments and verifies complete append-only chains.
type CheckpointStore struct {
	repository string
	appLogin   string
	keys       map[string]PublicKey
	signer     Signer
}

// DecodeKeySet strictly decodes the protected default-branch public-key file.
func DecodeKeySet(reader io.Reader, maxBytes int64) (KeySet, error) {
	if reader == nil || maxBytes <= 0 || maxBytes > 1<<20 {
		return KeySet{}, errors.New("checkpoint key-set input is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return KeySet{}, errors.New("read checkpoint key set")
	}
	if int64(len(body)) > maxBytes {
		return KeySet{}, errors.New("checkpoint key set exceeds byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var keySet KeySet
	if err := decoder.Decode(&keySet); err != nil {
		return KeySet{}, fmt.Errorf("decode checkpoint key set: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return KeySet{}, errors.New("checkpoint key set contains trailing JSON")
	}
	if keySet.SchemaVersion != 1 {
		return KeySet{}, errors.New("checkpoint key-set schema version is unsupported")
	}
	if len(keySet.Keys) > 32 {
		return KeySet{}, errors.New("checkpoint key set exceeds key limit")
	}
	if _, err := NewCheckpointStore("validation/repository", "validation[bot]", keySet, Signer{}); err != nil {
		return KeySet{}, err
	}
	return keySet, nil
}

// NewCheckpointStore constructs one repository- and App-bound store.
func NewCheckpointStore(
	repository string,
	appLogin string,
	keySet KeySet,
	signer Signer,
) (*CheckpointStore, error) {
	if repository == "" || appLogin == "" || keySet.SchemaVersion != 1 {
		return nil, errors.New("checkpoint store identity is invalid")
	}
	keys := make(map[string]PublicKey, len(keySet.Keys))
	var previousID string
	for index, key := range keySet.Keys {
		if key.ID == "" ||
			len(key.PublicKey) != ed25519.PublicKeySize ||
			key.NotBefore.IsZero() ||
			!key.NotAfter.After(key.NotBefore) ||
			index > 0 && key.ID <= previousID {
			return nil, errors.New("checkpoint public keys must be valid and strictly sorted")
		}
		if _, duplicate := keys[key.ID]; duplicate {
			return nil, errors.New("duplicate checkpoint key ID")
		}
		keys[key.ID] = key
		previousID = key.ID
	}
	if signer.KeyID != "" || len(signer.PrivateKey) != 0 {
		key, ok := keys[signer.KeyID]
		if !ok || len(signer.PrivateKey) != ed25519.PrivateKeySize ||
			!slices.Equal(
				key.PublicKey,
				signer.PrivateKey.Public().(ed25519.PublicKey),
			) {
			return nil, errors.New("checkpoint signer does not match public key set")
		}
	}
	return &CheckpointStore{
		repository: repository,
		appLogin:   appLogin,
		keys:       keys,
		signer:     signer,
	}, nil
}

// SignComment returns one append-only marker and its canonical checkpoint digest.
func (store *CheckpointStore) SignComment(
	checkpoint issueagentcontract.Checkpoint,
	summary string,
) (string, string, error) {
	if store == nil || store.signer.KeyID == "" ||
		len(store.signer.PrivateKey) != ed25519.PrivateKeySize {
		return "", "", errors.New("checkpoint signer is unavailable")
	}
	if checkpoint.Repository != store.repository {
		return "", "", errors.New("checkpoint repository does not match store")
	}
	if strings.TrimSpace(summary) == "" ||
		len(summary) > maxCheckpointSummary ||
		strings.Contains(summary, checkpointMarkerPrefix) {
		return "", "", errors.New("checkpoint summary is empty or unsafe")
	}
	canonical, err := issueagentcontract.CanonicalCheckpoint(checkpoint)
	if err != nil {
		return "", "", err
	}
	signature := ed25519.Sign(store.signer.PrivateKey, canonical)
	envelope := issueagentcontract.CheckpointEnvelope{
		SchemaVersion: 1,
		KeyID:         store.signer.KeyID,
		Checkpoint:    checkpoint,
		Signature:     base64.RawStdEncoding.EncodeToString(signature),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", "", fmt.Errorf("encode checkpoint envelope: %w", err)
	}
	body := checkpointMarkerPrefix + string(encoded) + checkpointMarkerSuffix +
		"\n\n" + summary
	return body, digestBytes(canonical), nil
}

// VerifyChain verifies every App-authored marker and returns the latest snapshot.
func (store *CheckpointStore) VerifyChain(
	comments []IssueComment,
	issueNumber int64,
	now time.Time,
) (VerifiedCheckpoint, error) {
	history, err := store.VerifyHistory(comments, issueNumber, now)
	if err != nil {
		return VerifiedCheckpoint{}, err
	}
	return history[len(history)-1], nil
}

// VerifyHistory verifies the complete append-only chain and returns every
// authoritative snapshot in sequence order for repository budget accounting.
func (store *CheckpointStore) VerifyHistory(
	comments []IssueComment,
	issueNumber int64,
	now time.Time,
) ([]VerifiedCheckpoint, error) {
	if store == nil || issueNumber <= 0 || now.IsZero() {
		return nil, errors.New("checkpoint verification input is invalid")
	}
	ordered := append([]IssueComment(nil), comments...)
	slices.SortFunc(ordered, func(left, right IssueComment) int {
		switch {
		case left.ID < right.ID:
			return -1
		case left.ID > right.ID:
			return 1
		default:
			return 0
		}
	})
	for index, comment := range ordered {
		if comment.ID <= 0 || index > 0 && comment.ID == ordered[index-1].ID {
			return nil, errors.New("checkpoint comments have invalid identity")
		}
	}
	recoverySkips, err := store.recoverySkips(ordered, issueNumber, now)
	if err != nil {
		return nil, err
	}

	var latest VerifiedCheckpoint
	var found bool
	var previousCommentID int64
	var previousDigest string
	var previousGeneration uint64
	var previousSequence uint64
	history := make([]VerifiedCheckpoint, 0)
	for _, comment := range ordered {
		if recoveryCommentID, skipped := recoverySkips[comment.ID]; skipped &&
			recoveryCommentID > comment.ID {
			continue
		}
		if !strings.Contains(comment.Body, checkpointMarkerPrefix) {
			continue
		}
		if comment.Author != store.appLogin || comment.AuthorType != "Bot" {
			// Public users may copy the hidden marker text. Only the configured
			// GitHub App identity can introduce authoritative chain entries.
			continue
		}
		found = true
		if comment.CreatedAt.IsZero() ||
			comment.UpdatedAt.IsZero() ||
			!comment.CreatedAt.Equal(comment.UpdatedAt) ||
			comment.CreatedAt.After(now) {
			return nil, errors.New("checkpoint comment was edited or has invalid time")
		}
		envelope, err := parseCheckpointComment(comment.Body)
		if err != nil {
			return nil, err
		}
		key, ok := store.keys[envelope.KeyID]
		if !ok ||
			comment.CreatedAt.Before(key.NotBefore) ||
			comment.CreatedAt.After(key.NotAfter) {
			return nil, errors.New("checkpoint key is unknown or outside its validity window")
		}
		checkpoint := envelope.Checkpoint
		if checkpoint.Repository != store.repository ||
			checkpoint.IssueNumber != issueNumber {
			return nil, errors.New("checkpoint object identity does not match Issue")
		}
		canonical, err := issueagentcontract.CanonicalCheckpoint(checkpoint)
		if err != nil {
			return nil, err
		}
		signature, err := base64.RawStdEncoding.DecodeString(envelope.Signature)
		if err != nil || !ed25519.Verify(key.PublicKey, canonical, signature) {
			return nil, errors.New("checkpoint signature is invalid")
		}
		digest := digestBytes(canonical)
		if previousCommentID == 0 {
			if checkpoint.Sequence != 1 ||
				checkpoint.ExpectedPreviousCheckpointID != nil ||
				checkpoint.PreviousCheckpointSHA256 != nil {
				return nil, errors.New("first checkpoint has an invalid predecessor")
			}
		} else {
			if checkpoint.Sequence != previousSequence+1 ||
				checkpoint.Generation < previousGeneration ||
				checkpoint.Generation > previousGeneration+1 ||
				checkpoint.ExpectedPreviousCheckpointID == nil ||
				*checkpoint.ExpectedPreviousCheckpointID != previousCommentID ||
				checkpoint.PreviousCheckpointSHA256 == nil ||
				*checkpoint.PreviousCheckpointSHA256 != previousDigest {
				return nil, errors.New("checkpoint chain is forked or out of order")
			}
			if checkpoint.Generation == previousGeneration {
				if err := issueagentcontract.ValidateCheckpointSuccessor(
					latest.Checkpoint, checkpoint,
				); err != nil {
					return nil, err
				}
			} else if err := issueagentcontract.ValidateCheckpointSuccessor(
				latest.Checkpoint, checkpoint,
			); err != nil {
				return nil, err
			}
		}
		latest = VerifiedCheckpoint{
			CommentID:  comment.ID,
			Digest:     digest,
			Checkpoint: checkpoint,
		}
		history = append(history, latest)
		previousCommentID = comment.ID
		previousDigest = digest
		previousGeneration = checkpoint.Generation
		previousSequence = checkpoint.Sequence
	}
	if !found {
		return nil, ErrNoCheckpoint
	}
	return history, nil
}

// QuarantineDigest binds the exact App-marker comments an admin recovery skips.
func QuarantineDigest(comments []IssueComment) (string, error) {
	if len(comments) == 0 || len(comments) > 128 {
		return "", errors.New("checkpoint quarantine is empty or oversized")
	}
	ordered := append([]IssueComment(nil), comments...)
	slices.SortFunc(ordered, func(left, right IssueComment) int {
		switch {
		case left.ID < right.ID:
			return -1
		case left.ID > right.ID:
			return 1
		default:
			return 0
		}
	})
	records := make([]struct {
		ID     int64  `json:"id"`
		SHA256 string `json:"sha256"`
	}, 0, len(ordered))
	for index, comment := range ordered {
		if comment.ID <= 0 || index > 0 && ordered[index-1].ID == comment.ID {
			return "", errors.New("checkpoint quarantine identity is invalid")
		}
		records = append(records, struct {
			ID     int64  `json:"id"`
			SHA256 string `json:"sha256"`
		}{ID: comment.ID, SHA256: digestBytes([]byte(comment.Body))})
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		return "", errors.New("encode checkpoint quarantine")
	}
	return digestBytes(encoded), nil
}

func (store *CheckpointStore) recoverySkips(
	ordered []IssueComment,
	issueNumber int64,
	now time.Time,
) (map[int64]int64, error) {
	byID := make(map[int64]IssueComment, len(ordered))
	for _, comment := range ordered {
		byID[comment.ID] = comment
	}
	skips := make(map[int64]int64)
	for _, recoveryComment := range ordered {
		if recoveryComment.Author != store.appLogin ||
			recoveryComment.AuthorType != "Bot" ||
			!strings.Contains(recoveryComment.Body, checkpointMarkerPrefix) {
			continue
		}
		recovery, recoveryDigest, err := store.verifyOneCheckpoint(
			recoveryComment, issueNumber, now,
		)
		if err != nil || recovery.Control == nil ||
			recovery.Control.Kind != "recover_chain" {
			continue
		}
		control := recovery.Control
		anchorComment, ok := byID[control.RecoveryAnchorCommentID]
		if !ok || anchorComment.ID >= recoveryComment.ID {
			return nil, errors.New("recovery checkpoint anchor is unavailable")
		}
		anchor, anchorDigest, err := store.verifyOneCheckpoint(
			anchorComment, issueNumber, now,
		)
		if err != nil || anchorDigest != control.RecoveryAnchorDigest ||
			recoveryDigest == "" ||
			recovery.Sequence != anchor.Sequence+1 ||
			recovery.Generation != anchor.Generation+1 ||
			recovery.ExpectedPreviousCheckpointID == nil ||
			*recovery.ExpectedPreviousCheckpointID != anchorComment.ID ||
			recovery.PreviousCheckpointSHA256 == nil ||
			*recovery.PreviousCheckpointSHA256 != anchorDigest {
			return nil, errors.New("recovery checkpoint does not bind its anchor")
		}
		quarantine := make([]IssueComment, 0)
		ids := make([]int64, 0)
		for _, candidate := range ordered {
			if candidate.ID <= anchorComment.ID ||
				candidate.ID >= recoveryComment.ID ||
				candidate.Author != store.appLogin ||
				candidate.AuthorType != "Bot" ||
				!strings.Contains(candidate.Body, checkpointMarkerPrefix) {
				continue
			}
			quarantine = append(quarantine, candidate)
			ids = append(ids, candidate.ID)
		}
		if !slices.Equal(ids, control.QuarantinedCommentIDs) {
			return nil, errors.New("recovery checkpoint omits an App marker")
		}
		digest, err := QuarantineDigest(quarantine)
		if err != nil || digest != control.QuarantineDigest {
			return nil, errors.New("recovery quarantine digest is invalid")
		}
		for _, id := range ids {
			if existing, duplicate := skips[id]; duplicate &&
				existing != recoveryComment.ID {
				return nil, errors.New("checkpoint belongs to conflicting recoveries")
			}
			skips[id] = recoveryComment.ID
		}
	}
	return skips, nil
}

func (store *CheckpointStore) verifyOneCheckpoint(
	comment IssueComment,
	issueNumber int64,
	now time.Time,
) (issueagentcontract.Checkpoint, string, error) {
	if comment.CreatedAt.IsZero() || comment.UpdatedAt.IsZero() ||
		!comment.CreatedAt.Equal(comment.UpdatedAt) ||
		comment.CreatedAt.After(now) {
		return issueagentcontract.Checkpoint{}, "", errors.New("checkpoint comment time is invalid")
	}
	envelope, err := parseCheckpointComment(comment.Body)
	if err != nil {
		return issueagentcontract.Checkpoint{}, "", err
	}
	key, ok := store.keys[envelope.KeyID]
	if !ok || comment.CreatedAt.Before(key.NotBefore) ||
		comment.CreatedAt.After(key.NotAfter) {
		return issueagentcontract.Checkpoint{}, "", errors.New("checkpoint key is unavailable")
	}
	checkpoint := envelope.Checkpoint
	if checkpoint.Repository != store.repository ||
		checkpoint.IssueNumber != issueNumber {
		return issueagentcontract.Checkpoint{}, "", errors.New("checkpoint identity is invalid")
	}
	canonical, err := issueagentcontract.CanonicalCheckpoint(checkpoint)
	if err != nil {
		return issueagentcontract.Checkpoint{}, "", err
	}
	signature, err := base64.RawStdEncoding.DecodeString(envelope.Signature)
	if err != nil || !ed25519.Verify(key.PublicKey, canonical, signature) {
		return issueagentcontract.Checkpoint{}, "", errors.New("checkpoint signature is invalid")
	}
	return checkpoint, digestBytes(canonical), nil
}

func parseCheckpointComment(body string) (issueagentcontract.CheckpointEnvelope, error) {
	if len(body) > maxCheckpointComment ||
		!strings.HasPrefix(body, checkpointMarkerPrefix) ||
		strings.Count(body, checkpointMarkerPrefix) != 1 {
		return issueagentcontract.CheckpointEnvelope{}, errors.New("checkpoint marker is malformed")
	}
	remainder := strings.TrimPrefix(body, checkpointMarkerPrefix)
	end := strings.Index(remainder, checkpointMarkerSuffix)
	if end < 0 {
		return issueagentcontract.CheckpointEnvelope{}, errors.New("checkpoint marker is unterminated")
	}
	encoded := remainder[:end]
	human := remainder[end+len(checkpointMarkerSuffix):]
	if strings.Contains(human, checkpointMarkerPrefix) {
		return issueagentcontract.CheckpointEnvelope{}, errors.New("checkpoint comment contains multiple markers")
	}
	return issueagentcontract.DecodeCheckpointEnvelope(
		strings.NewReader(encoded),
		int64(len(encoded)),
	)
}

func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
