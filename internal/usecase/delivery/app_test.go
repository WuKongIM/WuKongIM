package delivery

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
)

func TestSubmitCommittedClonesEventBeforeRuntime(t *testing.T) {
	runtime := &recordingRuntime{}
	app := New(Options{Runtime: runtime})
	event := messageevents.MessageCommitted{
		MessageID:         10,
		MessageSeq:        20,
		ChannelID:         "ch-1",
		ChannelType:       2,
		FromUID:           "sender",
		SenderSessionID:   30,
		ClientMsgNo:       "client-msg-no",
		Payload:           []byte("payload"),
		RedDot:            true,
		MessageScopedUIDs: []string{"u1", "u2"},
	}

	if err := app.SubmitCommitted(context.Background(), event); err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}

	event.Payload[0] = 'X'
	event.MessageScopedUIDs[0] = "changed"

	if len(runtime.committed) != 1 {
		t.Fatalf("runtime committed count = %d, want 1", len(runtime.committed))
	}
	got := runtime.committed[0]
	if string(got.Payload) != "payload" {
		t.Fatalf("runtime payload = %q, want %q", string(got.Payload), "payload")
	}
	if got.MessageScopedUIDs[0] != "u1" {
		t.Fatalf("runtime MessageScopedUIDs[0] = %q, want %q", got.MessageScopedUIDs[0], "u1")
	}
	if len(got.Payload) > 0 && len(event.Payload) > 0 && &got.Payload[0] == &event.Payload[0] {
		t.Fatal("runtime received payload sharing memory with caller event")
	}
	if len(got.MessageScopedUIDs) > 0 && len(event.MessageScopedUIDs) > 0 && &got.MessageScopedUIDs[0] == &event.MessageScopedUIDs[0] {
		t.Fatal("runtime received MessageScopedUIDs sharing memory with caller event")
	}
}

func TestFeedbackCommandsForwardToRuntime(t *testing.T) {
	runtime := &recordingRuntime{}
	app := New(Options{Runtime: runtime})
	recvack := RecvackCommand{
		UID:        "u1",
		SessionID:  11,
		MessageID:  22,
		MessageSeq: 33,
	}
	closed := SessionClosedCommand{
		UID:       "u2",
		SessionID: 44,
	}

	if err := app.Recvack(context.Background(), recvack); err != nil {
		t.Fatalf("Recvack() error = %v", err)
	}
	if err := app.SessionClosed(context.Background(), closed); err != nil {
		t.Fatalf("SessionClosed() error = %v", err)
	}

	if len(runtime.recvacks) != 1 {
		t.Fatalf("runtime recvack count = %d, want 1", len(runtime.recvacks))
	}
	if runtime.recvacks[0] != recvack {
		t.Fatalf("runtime recvack = %+v, want %+v", runtime.recvacks[0], recvack)
	}
	if len(runtime.closed) != 1 {
		t.Fatalf("runtime session-closed count = %d, want 1", len(runtime.closed))
	}
	if runtime.closed[0] != closed {
		t.Fatalf("runtime session-closed = %+v, want %+v", runtime.closed[0], closed)
	}
}

type recordingRuntime struct {
	committed []messageevents.MessageCommitted
	recvacks  []RecvackCommand
	closed    []SessionClosedCommand
}

func (r *recordingRuntime) SubmitCommitted(_ context.Context, event messageevents.MessageCommitted) error {
	r.committed = append(r.committed, event)
	return nil
}

func (r *recordingRuntime) Recvack(_ context.Context, cmd RecvackCommand) error {
	r.recvacks = append(r.recvacks, cmd)
	return nil
}

func (r *recordingRuntime) SessionClosed(_ context.Context, cmd SessionClosedCommand) error {
	r.closed = append(r.closed, cmd)
	return nil
}
