package issueagent

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// CheckpointEnvelope adds key identity and signature to canonical checkpoint data.
type CheckpointEnvelope struct {
	SchemaVersion int        `json:"schema_version"`
	KeyID         string     `json:"key_id"`
	Checkpoint    Checkpoint `json:"checkpoint"`
	Signature     string     `json:"signature"`
}

// ValidateCheckpointEnvelope checks structure but does not verify the signature.
func ValidateCheckpointEnvelope(envelope CheckpointEnvelope) error {
	if envelope.SchemaVersion != 1 {
		return errors.New("unsupported checkpoint envelope schema version")
	}
	if !keyIDPattern.MatchString(envelope.KeyID) {
		return errors.New("invalid checkpoint key ID")
	}
	signature, err := base64.RawStdEncoding.DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("invalid checkpoint signature encoding")
	}
	return ValidateCheckpoint(envelope.Checkpoint)
}

// DecodeCheckpointEnvelope decodes one bounded object and rejects unknown data.
func DecodeCheckpointEnvelope(reader io.Reader, maxBytes int64) (CheckpointEnvelope, error) {
	var envelope CheckpointEnvelope
	if err := decodeStrictJSON(reader, maxBytes, &envelope); err != nil {
		return CheckpointEnvelope{}, err
	}
	if err := ValidateCheckpointEnvelope(envelope); err != nil {
		return CheckpointEnvelope{}, err
	}
	return envelope, nil
}

func decodeStrictJSON(reader io.Reader, maxBytes int64, output any) error {
	if reader == nil || maxBytes <= 0 {
		return errors.New("JSON input limit must be positive")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return fmt.Errorf("read JSON input: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return errors.New("JSON input exceeds byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode JSON input: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("JSON input contains multiple values")
		}
		return fmt.Errorf("decode trailing JSON input: %w", err)
	}
	return nil
}
