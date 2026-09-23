package transfer

import (
	"encoding/base64"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// decodePublicationMetadata bounds allocation and validates the original clock
// during bundle preflight, before any imported row can change target storage.
func decodePublicationMetadata(raw string, serverTimestampMS int64) ([]byte, error) {
	if raw == "" {
		return nil, nil
	}
	if serverTimestampMS <= 0 {
		return nil, fmt.Errorf("%w: publication requires original source timestamp", ErrValidation)
	}
	if len(raw) > base64.StdEncoding.EncodedLen(publication.MaxEncodedBytes) {
		return nil, fmt.Errorf("%w: publication metadata exceeds limit", ErrValidation)
	}
	value, err := decodeBase64Field("publication_metadata_b64", raw)
	if err != nil {
		return nil, err
	}
	metadata, err := publication.Decode(value)
	if err != nil {
		return nil, fmt.Errorf("%w: publication metadata: %w", ErrValidation, err)
	}
	if _, _, err := metadata.ExpiryDeadlineMS(serverTimestampMS); err != nil {
		return nil, fmt.Errorf("%w: publication expiry: %w", ErrValidation, err)
	}
	return value, nil
}

// inspectPublicationMetadata keeps omitted native rows compatible while refusing
// a present malformed value instead of silently excluding it from transfer/proof.
func inspectPublicationMetadata(row map[string]any) ([]byte, error) {
	if _, present := row["publication_metadata"]; !present {
		return nil, nil
	}
	value, err := rowBytes(row, "publication_metadata")
	if err != nil || len(value) == 0 {
		return value, err
	}
	if _, err := publication.Decode(value); err != nil {
		return nil, fmt.Errorf("%w: publication metadata: %w", ErrValidation, err)
	}
	return value, nil
}
