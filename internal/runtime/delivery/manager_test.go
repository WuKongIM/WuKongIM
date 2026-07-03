package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/messageevents"
)

func TestManagerRequiresStartForFanoutRunnerByDefault(t *testing.T) {
	manager := NewManager(ManagerOptions{
		Planner: NewPlanner(PlannerOptions{}),
		Runner:  recordingManagerRunner{},
	})

	err := manager.SubmitCommitted(context.Background(), messageevents.MessageCommitted{MessageID: 1001})
	if !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("SubmitCommitted() error = %v, want ErrManagerClosed", err)
	}
}

func TestManagerPlansAndRunsFanout(t *testing.T) {
	presence := &fakePresenceResolver{
		routes: map[string][]Route{
			"u1": {{UID: "u1", OwnerNodeID: 10, SessionID: 101}},
			"u2": {{UID: "u2", OwnerNodeID: 20, SessionID: 201}},
		},
	}
	pusher := &fakePusher{}
	manager := NewManager(ManagerOptions{
		Planner: NewPlanner(PlannerOptions{
			Partitioner: staticPartitioner{
				partitions: []Partition{
					{ID: 1, LeaderNodeID: 10},
					{ID: 2, LeaderNodeID: 20},
				},
			},
		}),
		Runner: NewFanoutWorker(FanoutWorkerOptions{
			Subscribers: &fakeSubscriberPlanner{
				pages: []UIDPage{
					{UIDs: []string{"u1"}, Done: true},
					{UIDs: []string{"u2"}, Done: true},
				},
			},
			Presence: presence,
			Push:     pusher,
		}),
	})

	event := messageevents.MessageCommitted{MessageID: 1001, ChannelID: "c1"}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.SubmitCommitted(context.Background(), event); err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if len(pusher.commands) != 2 {
		t.Fatalf("push command count = %d, want 2; commands=%#v", len(pusher.commands), pusher.commands)
	}
	commands := commandsByOwner(pusher.commands)
	if _, ok := commands[10]; !ok {
		t.Fatalf("missing owner 10 push command: %#v", pusher.commands)
	}
	if _, ok := commands[20]; !ok {
		t.Fatalf("missing owner 20 push command: %#v", pusher.commands)
	}
}

func TestManagerRecvackClearsPending(t *testing.T) {
	manager := NewManager(ManagerOptions{})
	manager.BindPendingAck(PendingRecvAck{
		UID:        "u1",
		SessionID:  10,
		MessageID:  1001,
		MessageSeq: 20,
	})

	if count := manager.PendingAckCount(); count != 1 {
		t.Fatalf("PendingAckCount() = %d, want 1", count)
	}
	if err := manager.Recvack(context.Background(), Recvack{UID: "u1", SessionID: 10, MessageID: 1001, MessageSeq: 20}); err != nil {
		t.Fatalf("Recvack() error = %v", err)
	}
	if count := manager.PendingAckCount(); count != 0 {
		t.Fatalf("PendingAckCount() = %d, want 0", count)
	}
}

func TestManagerEnvelopeFromEventClonesSlices(t *testing.T) {
	pusher := &fakePusher{}
	manager := NewManager(ManagerOptions{
		Planner: NewPlanner(PlannerOptions{}),
		Runner: NewFanoutWorker(FanoutWorkerOptions{
			Presence: &fakePresenceResolver{
				routes: map[string][]Route{
					"u1": {{UID: "u1", OwnerNodeID: 10, SessionID: 101}},
				},
			},
			Push: pusher,
		}),
	})
	event := messageevents.MessageCommitted{
		MessageID:         1001,
		Payload:           []byte("payload"),
		MessageScopedUIDs: []string{"u1"},
	}

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.SubmitCommitted(context.Background(), event); err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	event.Payload[0] = 'X'
	event.MessageScopedUIDs[0] = "mutated"
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if len(pusher.commands) != 1 {
		t.Fatalf("push command count = %d, want 1", len(pusher.commands))
	}
	got := pusher.commands[0].Envelope
	if string(got.Payload) != "payload" {
		t.Fatalf("pushed payload = %q, want payload", string(got.Payload))
	}
	if got.MessageScopedUIDs[0] != "u1" {
		t.Fatalf("pushed scoped uid = %q, want u1", got.MessageScopedUIDs[0])
	}
}

func TestManagerEnvelopeFromEventCopiesSenderNodeID(t *testing.T) {
	pusher := &fakePusher{}
	manager := NewManager(ManagerOptions{
		Planner: NewPlanner(PlannerOptions{}),
		Runner: NewFanoutWorker(FanoutWorkerOptions{
			Presence: &fakePresenceResolver{
				routes: map[string][]Route{
					"sender": {
						{UID: "sender", OwnerNodeID: 10, SessionID: 100},
						{UID: "sender", OwnerNodeID: 20, SessionID: 100},
					},
				},
			},
			Push: pusher,
		}),
	})

	event := messageevents.MessageCommitted{
		MessageID:         1001,
		FromUID:           "sender",
		SenderNodeID:      10,
		SenderSessionID:   100,
		MessageScopedUIDs: []string{"sender"},
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.SubmitCommitted(context.Background(), event); err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if len(pusher.commands) != 1 {
		t.Fatalf("push command count = %d, want 1", len(pusher.commands))
	}
	if pusher.commands[0].OwnerNodeID != 20 {
		t.Fatalf("push owner = %d, want 20", pusher.commands[0].OwnerNodeID)
	}
	if got := pusher.commands[0].Envelope.SenderNodeID; got != 10 {
		t.Fatalf("pushed SenderNodeID = %d, want 10", got)
	}
}
