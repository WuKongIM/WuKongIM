package message

import (
	"context"
	"errors"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func TestCommitPreparedRowsBatchDuplicateReleasesLaterCheckpointLock(t *testing.T) {
	eng := openCompatEngine(t)
	id := channel.ChannelID{ID: "duplicate-checkpoint-release", Type: 1}
	first := mustForChannel(t, eng, "duplicate-checkpoint-release:1", id)
	second := mustForChannel(t, eng, "duplicate-checkpoint-release:1", id)
	defer first.Close()
	defer second.Close()
	entry := first.log.channelEntry
	entry.appendMu.Lock()
	entry.checkpointMu.Lock()

	err := commitPreparedRowsBatch(context.Background(), eng, []preparedCommitRows{
		{store: first},
		{store: second, checkpoint: &Checkpoint{HW: 1}},
	}, commitLaneFollowerApply)
	if !errors.Is(err, channel.ErrInvalidArgument) {
		t.Fatalf("commitPreparedRowsBatch() err = %v, want %v", err, channel.ErrInvalidArgument)
	}
	if !entry.checkpointMu.TryLock() {
		entry.checkpointMu.Unlock()
		t.Fatal("checkpoint lock remained held after duplicate rejection")
	}
	entry.checkpointMu.Unlock()
	if !entry.appendMu.TryLock() {
		t.Fatal("append lock remained held after duplicate rejection")
	}
	entry.appendMu.Unlock()
}

func TestPreparedCommitEntriesIncludesCheckpointOwnerFromDuplicateItem(t *testing.T) {
	eng := openCompatEngine(t)
	id := channel.ChannelID{ID: "duplicate-checkpoint-owner", Type: 1}
	first := mustForChannel(t, eng, "duplicate-checkpoint-owner:1", id)
	second := mustForChannel(t, eng, "duplicate-checkpoint-owner:1", id)
	defer first.Close()
	defer second.Close()

	appendEntries, checkpointEntries, duplicate := preparedCommitEntries([]preparedCommitRows{
		{store: first},
		{store: second, checkpoint: &Checkpoint{HW: 1}},
	})
	if !duplicate {
		t.Fatal("preparedCommitEntries() duplicate = false, want true")
	}
	if len(appendEntries) != 1 || appendEntries[0] != first.log.channelEntry {
		t.Fatalf("append entries = %v, want one canonical entry", appendEntries)
	}
	if len(checkpointEntries) != 1 || checkpointEntries[0] != first.log.channelEntry {
		t.Fatalf("checkpoint entries = %v, want later duplicate checkpoint owner", checkpointEntries)
	}
}
