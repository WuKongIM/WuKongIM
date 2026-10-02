package store

import (
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/publication"
)

// validatePublicationRecords keeps the memory backend aligned with the durable
// message codec. Validate the entire request before any records or HW can change.
func validatePublicationRecords(records []ch.Record) error {
	for _, record := range records {
		if len(record.PublicationMetadata) == 0 {
			continue
		}
		metadata, err := publication.Decode(record.PublicationMetadata)
		if err != nil || record.ServerTimestampMS <= 0 {
			return ch.ErrInvalidConfig
		}
		if _, _, err := metadata.ExpiryDeadlineMS(record.ServerTimestampMS); err != nil {
			return ch.ErrInvalidConfig
		}
	}
	return nil
}
