package delivery

import (
	"context"
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
		Envelope:  Envelope{MessageID: 1001, FromUID: "sender", SenderSessionID: 100},
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
}

func (p *fakePusher) Push(_ context.Context, command PushCommand) (PushResult, error) {
	command.Envelope = cloneEnvelope(command.Envelope)
	command.Routes = append([]Route(nil), command.Routes...)
	p.commands = append(p.commands, command)
	return PushResult{Accepted: append([]Route(nil), command.Routes...)}, nil
}

func commandsByOwner(commands []PushCommand) map[uint64]PushCommand {
	byOwner := make(map[uint64]PushCommand, len(commands))
	for _, command := range commands {
		byOwner[command.OwnerNodeID] = command
	}
	return byOwner
}
