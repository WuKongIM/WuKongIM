package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestPendingTrackerMatchingSendackResolvesPendingEntry(t *testing.T) {
	tracker := newPendingTracker()
	key := pendingKey{ClientSeq: 10, ClientMsgNo: "msg-10"}

	entry, err := tracker.add(key, time.Second)
	if err != nil {
		t.Fatalf("add() error = %v", err)
	}

	resolved := tracker.resolve(&frame.SendackPacket{
		ClientSeq:   10,
		ClientMsgNo: "msg-10",
		MessageID:   101,
		MessageSeq:  202,
		ReasonCode:  frame.ReasonSuccess,
	})
	if !resolved {
		t.Fatal("resolve() = false, want true")
	}

	result, err := waitPendingEntry(t, entry)
	if err != nil {
		t.Fatalf("pending result error = %v", err)
	}
	if result.ClientSeq != 10 {
		t.Fatalf("pending result ClientSeq = %d, want 10", result.ClientSeq)
	}
	if result.ClientMsgNo != "msg-10" {
		t.Fatalf("pending result ClientMsgNo = %q, want msg-10", result.ClientMsgNo)
	}
	if result.MessageID != 101 {
		t.Fatalf("pending result MessageID = %d, want 101", result.MessageID)
	}
	if result.MessageSeq != 202 {
		t.Fatalf("pending result MessageSeq = %d, want 202", result.MessageSeq)
	}
	if result.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("pending result ReasonCode = %s, want %s", result.ReasonCode, frame.ReasonSuccess)
	}
}

func TestPendingTrackerMatchingSendackFallsBackToClientSeq(t *testing.T) {
	tracker := newPendingTracker()
	key := pendingKey{ClientSeq: 11}

	entry, err := tracker.add(key, time.Second)
	if err != nil {
		t.Fatalf("add() error = %v", err)
	}

	resolved := tracker.resolve(&frame.SendackPacket{
		ClientSeq:   11,
		ClientMsgNo: "server-msg-11",
		MessageID:   111,
		MessageSeq:  222,
		ReasonCode:  frame.ReasonSuccess,
	})
	if !resolved {
		t.Fatal("resolve() = false, want true")
	}

	result, err := waitPendingEntry(t, entry)
	if err != nil {
		t.Fatalf("pending result error = %v", err)
	}
	if result.ClientMsgNo != "server-msg-11" {
		t.Fatalf("pending result ClientMsgNo = %q, want server-msg-11", result.ClientMsgNo)
	}
	if result.MessageID != 111 {
		t.Fatalf("pending result MessageID = %d, want 111", result.MessageID)
	}
}

func TestPendingTrackerNonSuccessSendackReturnsSendError(t *testing.T) {
	tracker := newPendingTracker()
	key := pendingKey{ClientSeq: 12, ClientMsgNo: "msg-12"}

	entry, err := tracker.add(key, time.Second)
	if err != nil {
		t.Fatalf("add() error = %v", err)
	}

	resolved := tracker.resolve(&frame.SendackPacket{
		ClientSeq:   12,
		ClientMsgNo: "msg-12",
		MessageID:   121,
		MessageSeq:  212,
		ReasonCode:  frame.ReasonAuthFail,
	})
	if !resolved {
		t.Fatal("resolve() = false, want true")
	}

	result, err := waitPendingEntry(t, entry)
	var sendErr SendError
	if !errors.As(err, &sendErr) {
		t.Fatalf("pending result error = %T %[1]v, want SendError", err)
	}
	if sendErr.ClientSeq != 12 {
		t.Fatalf("SendError ClientSeq = %d, want 12", sendErr.ClientSeq)
	}
	if sendErr.ClientMsgNo != "msg-12" {
		t.Fatalf("SendError ClientMsgNo = %q, want msg-12", sendErr.ClientMsgNo)
	}
	if sendErr.ReasonCode != frame.ReasonAuthFail {
		t.Fatalf("SendError ReasonCode = %s, want %s", sendErr.ReasonCode, frame.ReasonAuthFail)
	}
	if result.ClientSeq != 12 {
		t.Fatalf("pending result ClientSeq = %d, want 12", result.ClientSeq)
	}
	if result.ClientMsgNo != "msg-12" {
		t.Fatalf("pending result ClientMsgNo = %q, want msg-12", result.ClientMsgNo)
	}
	if result.MessageID != 121 {
		t.Fatalf("pending result MessageID = %d, want 121", result.MessageID)
	}
	if result.MessageSeq != 212 {
		t.Fatalf("pending result MessageSeq = %d, want 212", result.MessageSeq)
	}
	if result.ReasonCode != frame.ReasonAuthFail {
		t.Fatalf("pending result ReasonCode = %s, want %s", result.ReasonCode, frame.ReasonAuthFail)
	}
}

func TestPendingTrackerCloseFailsAllEntriesWithErrClosed(t *testing.T) {
	tracker := newPendingTracker()
	first, err := tracker.add(pendingKey{ClientSeq: 13, ClientMsgNo: "msg-13"}, time.Second)
	if err != nil {
		t.Fatalf("add(first) error = %v", err)
	}
	second, err := tracker.add(pendingKey{ClientSeq: 14, ClientMsgNo: "msg-14"}, time.Second)
	if err != nil {
		t.Fatalf("add(second) error = %v", err)
	}

	tracker.close(ErrClosed)

	if _, err = waitPendingEntry(t, first); !errors.Is(err, ErrClosed) {
		t.Fatalf("first pending error = %v, want %v", err, ErrClosed)
	}
	if _, err = waitPendingEntry(t, second); !errors.Is(err, ErrClosed) {
		t.Fatalf("second pending error = %v, want %v", err, ErrClosed)
	}

	_, err = tracker.add(pendingKey{ClientSeq: 15}, time.Second)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("add() after close error = %v, want %v", err, ErrClosed)
	}
}

func TestPendingTrackerAddTimeoutFailsWithErrAckTimeout(t *testing.T) {
	tracker := newPendingTracker()
	entry, err := tracker.add(pendingKey{ClientSeq: 16, ClientMsgNo: "msg-16"}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("add() error = %v", err)
	}

	if _, err = waitPendingEntry(t, entry); !errors.Is(err, ErrAckTimeout) {
		t.Fatalf("pending error = %v, want %v", err, ErrAckTimeout)
	}
}

func TestPendingTrackerResolveReturnsFalseForNilAndUnmatchedAck(t *testing.T) {
	tracker := newPendingTracker()

	if tracker.resolve(nil) {
		t.Fatal("resolve(nil) = true, want false")
	}
	if tracker.resolve(&frame.SendackPacket{
		ClientSeq:   404,
		ClientMsgNo: "missing",
		ReasonCode:  frame.ReasonSuccess,
	}) {
		t.Fatal("resolve(unmatched) = true, want false")
	}
}

func TestPendingTrackerDuplicateAddRejectsWithoutOrphaningOriginal(t *testing.T) {
	tracker := newPendingTracker()
	key := pendingKey{ClientSeq: 17, ClientMsgNo: "msg-17"}

	first, err := tracker.add(key, time.Second)
	if err != nil {
		t.Fatalf("add(first) error = %v", err)
	}
	second, err := tracker.add(key, 10*time.Millisecond)
	if !errors.Is(err, ErrDuplicatePendingSend) {
		t.Fatalf("add(second) error = %v, want %v", err, ErrDuplicatePendingSend)
	}
	if second != nil {
		t.Fatalf("add(second) entry = %v, want nil", second)
	}

	resolved := tracker.resolve(&frame.SendackPacket{
		ClientSeq:   17,
		ClientMsgNo: "msg-17",
		MessageID:   171,
		MessageSeq:  271,
		ReasonCode:  frame.ReasonSuccess,
	})
	if !resolved {
		t.Fatal("resolve() = false, want true")
	}

	result, err := waitPendingEntry(t, first)
	if err != nil {
		t.Fatalf("first pending result error = %v", err)
	}
	if result.MessageID != 171 {
		t.Fatalf("first pending result MessageID = %d, want 171", result.MessageID)
	}
	if result.MessageSeq != 271 {
		t.Fatalf("first pending result MessageSeq = %d, want 271", result.MessageSeq)
	}
}

func waitPendingEntry(t *testing.T, entry *pendingEntry) (SendResult, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	select {
	case out := <-entry.done:
		return out.result, out.err
	case <-ctx.Done():
		t.Fatal("pending entry did not resolve")
	}
	return SendResult{}, nil
}
