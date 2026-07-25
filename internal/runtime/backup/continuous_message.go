package backup

import (
	"fmt"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// ObservedMessageBoundary is one exact committed Channel cut, independent of
// cluster routing and transport details.
type ObservedMessageBoundary struct {
	// ChannelID and ChannelType identify the logical message log.
	ChannelID   string
	ChannelType uint8
	// Epoch and LogStartOffset fence the retained log generation.
	Epoch          uint64
	LogStartOffset uint64
	// HW is the exact committed sequence included by the cut.
	HW uint64
	// ObservedAtUnixMillis is the UTC observation time.
	ObservedAtUnixMillis int64
}

// PendingMessageWork returns the exact number of payload or boundary records
// required to advance previous to current.
func PendingMessageWork(previous backupartifact.ChannelBoundary, current ObservedMessageBoundary) (uint64, error) {
	if current.ChannelID == "" || current.Epoch == 0 ||
		current.LogStartOffset > current.HW || current.HW == ^uint64(0) ||
		current.ObservedAtUnixMillis <= 0 {
		return 0, ErrInvalidCapture
	}
	if previous.ChannelID == "" {
		return current.HW - current.LogStartOffset, nil
	}
	if previous.ChannelID != current.ChannelID || previous.ChannelType != current.ChannelType ||
		current.Epoch < previous.Epoch || current.HW < previous.HW ||
		current.LogStartOffset < previous.LogStartOffset ||
		current.LogStartOffset > previous.HW {
		return 0, ErrSourceRegressed
	}
	if current.HW > previous.HW {
		return current.HW - previous.HW, nil
	}
	if current.Epoch != previous.Epoch || current.LogStartOffset != previous.LogStartOffset {
		return 1, nil
	}
	return 0, nil
}

// FirstPendingMessageSeq returns the first payload sequence needed for current.
// A value above current.HW means the work is a boundary-only change.
func FirstPendingMessageSeq(previous backupartifact.ChannelBoundary, current ObservedMessageBoundary) (uint64, error) {
	if _, err := PendingMessageWork(previous, current); err != nil {
		return 0, err
	}
	if current.LogStartOffset == ^uint64(0) {
		return 0, fmt.Errorf("%w: message sequence overflow", ErrInvalidCapture)
	}
	first := current.LogStartOffset + 1
	if previous.ChannelID != "" && previous.HW < ^uint64(0) && previous.HW+1 > first {
		first = previous.HW + 1
	}
	return first, nil
}
