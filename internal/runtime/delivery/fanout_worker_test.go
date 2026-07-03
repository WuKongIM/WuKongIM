package delivery

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestFanoutWorkerResolvesPresenceAndPushesByOwner(t *testing.T) {
	subscribers := &fakeSubscriberPlanner{
		pages: []UIDPage{
			{UIDs: []string{"u1", "u2", "u3"}, Done: true},
		},
	}
	presence := &fakePresenceResolver{
		routes: map[string][]Route{
			"u1": {{UID: "u1", OwnerNodeID: 10, SessionID: 101}},
			"u2": {{UID: "u2", OwnerNodeID: 20, SessionID: 201}},
		},
	}
	pusher := &fakePusher{}
	worker := NewFanoutWorker(FanoutWorkerOptions{
		Subscribers: subscribers,
		Presence:    presence,
		Push:        pusher,
		PageSize:    2,
	})

	err := worker.RunTask(context.Background(), FanoutTask{Envelope: Envelope{MessageID: 1001}, Partition: Partition{ID: 1}, Attempt: 1})
	if err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}

	if subscribers.calls != 1 {
		t.Fatalf("subscriber calls = %d, want 1", subscribers.calls)
	}
	if !reflect.DeepEqual(presence.seenUIDs, [][]string{{"u1", "u2", "u3"}}) {
		t.Fatalf("presence seen UIDs = %#v, want u1,u2,u3", presence.seenUIDs)
	}
	commands := commandsByOwner(pusher.commands)
	if len(commands) != 2 {
		t.Fatalf("push owner count = %d, want 2; commands=%#v", len(commands), pusher.commands)
	}
	if got := commands[10].Routes; len(got) != 1 || got[0].UID != "u1" {
		t.Fatalf("owner 10 routes = %#v, want u1", got)
	}
	if got := commands[20].Routes; len(got) != 1 || got[0].UID != "u2" {
		t.Fatalf("owner 20 routes = %#v, want u2", got)
	}
}

func TestFanoutWorkerUsesMessageScopedUIDsWithoutSubscriberScan(t *testing.T) {
	subscribers := &fakeSubscriberPlanner{}
	presence := &fakePresenceResolver{
		routes: map[string][]Route{
			"u2": {{UID: "u2", OwnerNodeID: 20, SessionID: 201}},
		},
	}
	pusher := &fakePusher{}
	worker := NewFanoutWorker(FanoutWorkerOptions{
		Subscribers: subscribers,
		Presence:    presence,
		Push:        pusher,
	})

	task := FanoutTask{
		Envelope: Envelope{
			MessageID:         1001,
			MessageScopedUIDs: []string{"u2", "u3"},
		},
		Partition: Partition{ID: 1},
		Attempt:   1,
	}
	if err := worker.RunTask(context.Background(), task); err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}

	if subscribers.calls != 0 {
		t.Fatalf("subscriber calls = %d, want 0", subscribers.calls)
	}
	if !reflect.DeepEqual(presence.seenUIDs, [][]string{{"u2", "u3"}}) {
		t.Fatalf("presence seen UIDs = %#v, want scoped UIDs", presence.seenUIDs)
	}
	if len(pusher.commands) != 1 || pusher.commands[0].OwnerNodeID != 20 {
		t.Fatalf("push commands = %#v, want one owner 20 command", pusher.commands)
	}
}

func TestFanoutWorkerSplitsOwnerPushBatches(t *testing.T) {
	presence := &fakePresenceResolver{
		routes: map[string][]Route{
			"u1": {{UID: "u1", OwnerNodeID: 10, SessionID: 101}},
			"u2": {{UID: "u2", OwnerNodeID: 10, SessionID: 102}},
			"u3": {{UID: "u3", OwnerNodeID: 10, SessionID: 103}},
		},
	}
	pusher := &fakePusher{}
	worker := NewFanoutWorker(FanoutWorkerOptions{
		Presence:      presence,
		Push:          pusher,
		PushBatchSize: 2,
	})

	task := FanoutTask{
		Envelope: Envelope{
			MessageID:         1001,
			MessageScopedUIDs: []string{"u1", "u2", "u3"},
		},
		Partition: Partition{ID: 1},
		Attempt:   1,
	}
	if err := worker.RunTask(context.Background(), task); err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}

	if len(pusher.commands) != 2 {
		t.Fatalf("push command count = %d, want 2; commands=%#v", len(pusher.commands), pusher.commands)
	}
	if got := len(pusher.commands[0].Routes); got != 2 {
		t.Fatalf("first push route count = %d, want 2", got)
	}
	if got := len(pusher.commands[1].Routes); got != 1 {
		t.Fatalf("second push route count = %d, want 1", got)
	}
	for i, command := range pusher.commands {
		if command.OwnerNodeID != 10 {
			t.Fatalf("command[%d] owner = %d, want 10", i, command.OwnerNodeID)
		}
	}
}

func TestFanoutWorkerReturnsErrorForRetryablePush(t *testing.T) {
	presence := &fakePresenceResolver{
		routes: map[string][]Route{
			"u1": {{UID: "u1", OwnerNodeID: 10, SessionID: 101}},
		},
	}
	pusher := &fakePusher{result: PushResult{Retryable: []Route{{UID: "u1", OwnerNodeID: 10, SessionID: 101}}}}
	worker := NewFanoutWorker(FanoutWorkerOptions{
		Presence: presence,
		Push:     pusher,
	})

	err := worker.RunTask(context.Background(), FanoutTask{
		Envelope:  Envelope{MessageID: 1001, MessageScopedUIDs: []string{"u1"}},
		Partition: Partition{ID: 1},
		Attempt:   1,
	})
	if !errors.Is(err, ErrRetryablePushRoutes) {
		t.Fatalf("RunTask() error = %v, want ErrRetryablePushRoutes", err)
	}
}

func TestFanoutWorkerContinuesAfterRetryablePushBatch(t *testing.T) {
	presence := &fakePresenceResolver{
		routes: map[string][]Route{
			"u1": {{UID: "u1", OwnerNodeID: 10, SessionID: 101}},
			"u2": {{UID: "u2", OwnerNodeID: 20, SessionID: 201}},
		},
	}
	pusher := &firstRetryablePusher{}
	worker := NewFanoutWorker(FanoutWorkerOptions{
		Presence: presence,
		Push:     pusher,
	})

	err := worker.RunTask(context.Background(), FanoutTask{
		Envelope:  Envelope{MessageID: 1001, MessageScopedUIDs: []string{"u1", "u2"}},
		Partition: Partition{ID: 1},
		Attempt:   1,
	})
	if !errors.Is(err, ErrRetryablePushRoutes) {
		t.Fatalf("RunTask() error = %v, want ErrRetryablePushRoutes", err)
	}
	if len(pusher.commands) != 2 {
		t.Fatalf("push command count = %d, want 2; commands=%#v", len(pusher.commands), pusher.commands)
	}
	var retryErr *RetryablePushRoutesError
	if !errors.As(err, &retryErr) {
		t.Fatalf("RunTask() error = %T, want *RetryablePushRoutesError", err)
	}
	retryRoutes := retryErr.Routes()
	if len(retryRoutes) != 1 || retryRoutes[0].UID != pusher.commands[0].Routes[0].UID {
		t.Fatalf("retry routes = %#v, want first pushed route", retryRoutes)
	}
}

func TestFanoutWorkerSkipsSenderSameSession(t *testing.T) {
	subscribers := &fakeSubscriberPlanner{
		pages: []UIDPage{
			{UIDs: []string{"sender"}, Done: true},
		},
	}
	presence := &fakePresenceResolver{
		routes: map[string][]Route{
			"sender": {
				{UID: "sender", OwnerNodeID: 10, SessionID: 100},
				{UID: "sender", OwnerNodeID: 10, SessionID: 101},
			},
		},
	}
	pusher := &fakePusher{}
	worker := NewFanoutWorker(FanoutWorkerOptions{
		Subscribers: subscribers,
		Presence:    presence,
		Push:        pusher,
	})

	task := FanoutTask{
		Envelope:  Envelope{MessageID: 1001, FromUID: "sender", SenderNodeID: 10, SenderSessionID: 100},
		Partition: Partition{ID: 1},
		Attempt:   1,
	}
	if err := worker.RunTask(context.Background(), task); err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}

	if len(pusher.commands) != 1 {
		t.Fatalf("push command count = %d, want 1", len(pusher.commands))
	}
	routes := pusher.commands[0].Routes
	if len(routes) != 1 || routes[0].SessionID != 101 {
		t.Fatalf("pushed routes = %#v, want only different sender session 101", routes)
	}
}

func TestFanoutWorkerInvalidSubscriberCursorReturnsError(t *testing.T) {
	tests := []struct {
		name string
		page UIDPage
	}{
		{
			name: "empty cursor",
			page: UIDPage{UIDs: []string{"u1"}, Done: false},
		},
		{
			name: "repeated cursor",
			page: UIDPage{UIDs: []string{"u1"}, NextCursor: "cursor-1", Done: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subscribers := &fakeSubscriberPlanner{pages: []UIDPage{tt.page}}
			worker := NewFanoutWorker(FanoutWorkerOptions{
				Subscribers: subscribers,
				Presence: &fakePresenceResolver{
					routes: map[string][]Route{
						"u1": {{UID: "u1", OwnerNodeID: 10, SessionID: 101}},
					},
				},
				Push: &fakePusher{},
			})

			err := worker.RunTask(context.Background(), FanoutTask{
				Envelope:  Envelope{MessageID: 1001},
				Partition: Partition{ID: 1},
				Cursor:    "cursor-1",
				Attempt:   1,
			})
			if !errors.Is(err, ErrInvalidSubscriberCursor) {
				t.Fatalf("RunTask() error = %v, want ErrInvalidSubscriberCursor", err)
			}
		})
	}
}

func TestFanoutWorkerSkipsSenderSameSessionOnlyOnSenderOwner(t *testing.T) {
	subscribers := &fakeSubscriberPlanner{
		pages: []UIDPage{
			{UIDs: []string{"sender"}, Done: true},
		},
	}
	presence := &fakePresenceResolver{
		routes: map[string][]Route{
			"sender": {
				{UID: "sender", OwnerNodeID: 10, SessionID: 100},
				{UID: "sender", OwnerNodeID: 20, SessionID: 100},
			},
		},
	}
	pusher := &fakePusher{}
	worker := NewFanoutWorker(FanoutWorkerOptions{
		Subscribers: subscribers,
		Presence:    presence,
		Push:        pusher,
	})

	task := FanoutTask{
		Envelope:  Envelope{MessageID: 1001, FromUID: "sender", SenderNodeID: 10, SenderSessionID: 100},
		Partition: Partition{ID: 1},
		Attempt:   1,
	}
	if err := worker.RunTask(context.Background(), task); err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}

	if len(pusher.commands) != 1 {
		t.Fatalf("push command count = %d, want 1", len(pusher.commands))
	}
	if pusher.commands[0].OwnerNodeID != 20 {
		t.Fatalf("push owner = %d, want 20", pusher.commands[0].OwnerNodeID)
	}
	routes := pusher.commands[0].Routes
	if len(routes) != 1 || routes[0].OwnerNodeID != 20 || routes[0].SessionID != 100 {
		t.Fatalf("pushed routes = %#v, want same session on another owner", routes)
	}
}

type fakeSubscriberPlanner struct {
	pages []UIDPage
	calls int
}

func (p *fakeSubscriberPlanner) NextPartitionPage(_ context.Context, _ FanoutTask, _ string, _ int) (UIDPage, error) {
	p.calls++
	if len(p.pages) == 0 {
		return UIDPage{Done: true}, nil
	}
	page := p.pages[0]
	p.pages = p.pages[1:]
	return page, nil
}

type fakePresenceResolver struct {
	routes   map[string][]Route
	seenUIDs [][]string
}

func (r *fakePresenceResolver) EndpointsByUIDs(_ context.Context, uids []string) (map[string][]Route, error) {
	r.seenUIDs = append(r.seenUIDs, append([]string(nil), uids...))
	result := make(map[string][]Route, len(uids))
	for _, uid := range uids {
		routes := r.routes[uid]
		result[uid] = append([]Route(nil), routes...)
	}
	return result, nil
}

type fakePusher struct {
	commands []PushCommand
	result   PushResult
}

func (p *fakePusher) Push(_ context.Context, command PushCommand) (PushResult, error) {
	command.Envelope = cloneEnvelope(command.Envelope)
	command.Routes = append([]Route(nil), command.Routes...)
	p.commands = append(p.commands, command)
	if len(p.result.Accepted) > 0 || len(p.result.Retryable) > 0 || len(p.result.Dropped) > 0 {
		return p.result, nil
	}
	return PushResult{Accepted: append([]Route(nil), command.Routes...)}, nil
}

type firstRetryablePusher struct {
	commands []PushCommand
}

func (p *firstRetryablePusher) Push(_ context.Context, command PushCommand) (PushResult, error) {
	command.Envelope = cloneEnvelope(command.Envelope)
	command.Routes = append([]Route(nil), command.Routes...)
	p.commands = append(p.commands, command)
	if len(p.commands) == 1 {
		return PushResult{Retryable: append([]Route(nil), command.Routes...)}, nil
	}
	return PushResult{Accepted: append([]Route(nil), command.Routes...)}, nil
}

func commandsByOwner(commands []PushCommand) map[uint64]PushCommand {
	byOwner := make(map[uint64]PushCommand, len(commands))
	for _, command := range commands {
		byOwner[command.OwnerNodeID] = command
	}
	return byOwner
}
