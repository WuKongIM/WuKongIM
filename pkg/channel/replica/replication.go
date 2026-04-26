package replica

import "github.com/WuKongIM/WuKongIM/pkg/channel"

func validateFetchedRecordIndexes(records []channel.Record, firstIndex uint64) error {
	for i, record := range records {
		expected := firstIndex + uint64(i)
		if record.Index != expected {
			return channel.ErrCorruptState
		}
	}
	return nil
}
