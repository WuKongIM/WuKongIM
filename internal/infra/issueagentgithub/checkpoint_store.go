package issueagentgithub

import (
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
	decoder := json.NewDecoder(io.LimitReader(reader, maxBytes+1))
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
	if store == nil || issueNumber <= 0 || now.IsZero() {
		return VerifiedCheckpoint{}, errors.New("checkpoint verification input is invalid")
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

	var latest VerifiedCheckpoint
	var found bool
	var previousCommentID int64
	var previousDigest string
	var previousGeneration uint64
	var previousSequence uint64
	for index, comment := range ordered {
		if comment.ID <= 0 || index > 0 && comment.ID == ordered[index-1].ID {
			return VerifiedCheckpoint{}, errors.New("checkpoint comments have invalid identity")
		}
		if !strings.Contains(comment.Body, checkpointMarkerPrefix) {
			continue
		}
		found = true
		if comment.Author != store.appLogin || comment.AuthorType != "Bot" {
			return VerifiedCheckpoint{}, errors.New("checkpoint marker has an unexpected author")
		}
		if comment.CreatedAt.IsZero() ||
			comment.UpdatedAt.IsZero() ||
			!comment.CreatedAt.Equal(comment.UpdatedAt) ||
			comment.CreatedAt.After(now) {
			return VerifiedCheckpoint{}, errors.New("checkpoint comment was edited or has invalid time")
		}
		envelope, err := parseCheckpointComment(comment.Body)
		if err != nil {
			return VerifiedCheckpoint{}, err
		}
		key, ok := store.keys[envelope.KeyID]
		if !ok ||
			comment.CreatedAt.Before(key.NotBefore) ||
			comment.CreatedAt.After(key.NotAfter) {
			return VerifiedCheckpoint{}, errors.New("checkpoint key is unknown or outside its validity window")
		}
		checkpoint := envelope.Checkpoint
		if checkpoint.Repository != store.repository ||
			checkpoint.IssueNumber != issueNumber {
			return VerifiedCheckpoint{}, errors.New("checkpoint object identity does not match Issue")
		}
		canonical, err := issueagentcontract.CanonicalCheckpoint(checkpoint)
		if err != nil {
			return VerifiedCheckpoint{}, err
		}
		signature, err := base64.RawStdEncoding.DecodeString(envelope.Signature)
		if err != nil || !ed25519.Verify(key.PublicKey, canonical, signature) {
			return VerifiedCheckpoint{}, errors.New("checkpoint signature is invalid")
		}
		digest := digestBytes(canonical)
		if previousCommentID == 0 {
			if checkpoint.Sequence != 1 ||
				checkpoint.ExpectedPreviousCheckpointID != nil ||
				checkpoint.PreviousCheckpointSHA256 != nil {
				return VerifiedCheckpoint{}, errors.New("first checkpoint has an invalid predecessor")
			}
		} else {
			if checkpoint.Sequence != previousSequence+1 ||
				checkpoint.Generation < previousGeneration ||
				checkpoint.Generation > previousGeneration+1 ||
				checkpoint.ExpectedPreviousCheckpointID == nil ||
				*checkpoint.ExpectedPreviousCheckpointID != previousCommentID ||
				checkpoint.PreviousCheckpointSHA256 == nil ||
				*checkpoint.PreviousCheckpointSHA256 != previousDigest {
				return VerifiedCheckpoint{}, errors.New("checkpoint chain is forked or out of order")
			}
			if checkpoint.Generation == previousGeneration {
				if err := issueagentcontract.ValidateTransition(
					latest.Checkpoint.State, checkpoint.State,
				); err != nil {
					return VerifiedCheckpoint{}, err
				}
			}
		}
		latest = VerifiedCheckpoint{
			CommentID:  comment.ID,
			Digest:     digest,
			Checkpoint: checkpoint,
		}
		previousCommentID = comment.ID
		previousDigest = digest
		previousGeneration = checkpoint.Generation
		previousSequence = checkpoint.Sequence
	}
	if !found {
		return VerifiedCheckpoint{}, ErrNoCheckpoint
	}
	return latest, nil
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
