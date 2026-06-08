package recipient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	"github.com/WuKongIM/WuKongIM/internalv2/contracts/messageevents"
)

func TestDispatcherUsesScopedRecipientsDirectly(t *testing.T) {
	local := &recordingLocalProcessor{}
	dispatcher := NewDispatcher(DispatcherOptions{
		LocalNodeID: 1,
		Recipients:  &panicRecipientSource{t: t},
		Resolver: &mappingRecipientAuthorityResolver{
			targets: map[string]authority.Target{
				"u1": localTarget(1),
				"u2": localTarget(1),
			},
		},
		Local: local,
	})

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		MessageID:         100,
		MessageSeq:        9,
		ChannelID:         "group-1",
		ChannelType:       2,
		MessageScopedUIDs: []string{"u1", "u2"},
	})
	if err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if len(local.requests) != 1 {
		t.Fatalf("local requests = %d, want 1", len(local.requests))
	}
	wantRecipients := []Recipient{{UID: "u1"}, {UID: "u2"}}
	if !reflect.DeepEqual(local.requests[0].Recipients, wantRecipients) {
		t.Fatalf("local recipients = %#v, want %#v", local.requests[0].Recipients, wantRecipients)
	}
}

func TestDispatcherPagesSubscribersWithoutLoadingAll(t *testing.T) {
	var order []string
	source := &recordingRecipientSource{
		pages: []RecipientPage{
			{
				Recipients: []Recipient{{UID: "u1"}},
				Cursor:     "next",
			},
			{
				Recipients: []Recipient{{UID: "u2"}},
				Done:       true,
			},
		},
		onCall: func(cursor string) {
			order = append(order, "source:"+cursor)
		},
	}
	local := &recordingLocalProcessor{
		onProcess: func(req ProcessRequest) {
			order = append(order, "process:"+req.Recipients[0].UID)
		},
	}
	dispatcher := NewDispatcher(DispatcherOptions{
		LocalNodeID: 1,
		Recipients:  source,
		Resolver: &mappingRecipientAuthorityResolver{
			targets: map[string]authority.Target{
				"u1": localTarget(1),
				"u2": localTarget(1),
			},
		},
		Local: local,
	})

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:   "group-1",
		ChannelType: 2,
	})
	if err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	wantOrder := []string{"source:", "process:u1", "source:next", "process:u2"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("order = %#v, want %#v", order, wantOrder)
	}
	if len(source.calls) != 2 {
		t.Fatalf("source calls = %d, want 2", len(source.calls))
	}
}

func TestDispatcherPassesConfiguredPageSizeToSource(t *testing.T) {
	source := &recordingRecipientSource{
		pages: []RecipientPage{
			{Done: true},
		},
	}
	dispatcher := NewDispatcher(DispatcherOptions{
		LocalNodeID: 1,
		Recipients:  source,
		PageSize:    17,
	})

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:   "group-1",
		ChannelType: 2,
	})
	if err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if len(source.calls) != 1 {
		t.Fatalf("source calls = %d, want 1", len(source.calls))
	}
	if source.calls[0].limit != 17 {
		t.Fatalf("source limit = %d, want 17", source.calls[0].limit)
	}
}

func TestDispatcherRequiresRecipientSourceForUnscopedEvents(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherOptions{LocalNodeID: 1})

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:   "group-1",
		ChannelType: 2,
	})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("SubmitCommitted() error = %v, want %v", err, ErrRouteNotReady)
	}
}

func TestDispatcherGroupsRecipientsByAuthorityTarget(t *testing.T) {
	local := &recordingLocalProcessor{}
	remote := &recordingRecipientRemote{}
	localAuthority := localTarget(1)
	remoteAuthority := localTarget(2)
	dispatcher := NewDispatcher(DispatcherOptions{
		LocalNodeID: 1,
		Resolver: &mappingRecipientAuthorityResolver{
			targets: map[string]authority.Target{
				"u1": localAuthority,
				"u2": remoteAuthority,
				"u3": localAuthority,
			},
		},
		Local:  local,
		Remote: remote,
	})

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:         "group-1",
		ChannelType:       2,
		MessageScopedUIDs: []string{"u1", "u2", "u3"},
	})
	if err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if len(local.requests) != 1 {
		t.Fatalf("local requests = %d, want 1", len(local.requests))
	}
	if local.requests[0].Target != localAuthority {
		t.Fatalf("local target = %#v, want %#v", local.requests[0].Target, localAuthority)
	}
	wantLocalRecipients := []Recipient{{UID: "u1"}, {UID: "u3"}}
	if !reflect.DeepEqual(local.requests[0].Recipients, wantLocalRecipients) {
		t.Fatalf("local recipients = %#v, want %#v", local.requests[0].Recipients, wantLocalRecipients)
	}
	if len(remote.requests) != 1 {
		t.Fatalf("remote requests = %d, want 1", len(remote.requests))
	}
	if remote.requests[0].Target != remoteAuthority {
		t.Fatalf("remote target = %#v, want %#v", remote.requests[0].Target, remoteAuthority)
	}
	wantRemoteRecipients := []Recipient{{UID: "u2"}}
	if !reflect.DeepEqual(remote.requests[0].Recipients, wantRemoteRecipients) {
		t.Fatalf("remote recipients = %#v, want %#v", remote.requests[0].Recipients, wantRemoteRecipients)
	}
}

func TestDispatcherRejectsInvalidResolvedTarget(t *testing.T) {
	local := &recordingLocalProcessor{}
	remote := &recordingRecipientRemote{}
	dispatcher := NewDispatcher(DispatcherOptions{
		LocalNodeID: 1,
		Resolver: &mappingRecipientAuthorityResolver{
			targets: map[string]authority.Target{
				"u1": {},
			},
		},
		Local:  local,
		Remote: remote,
	})

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:         "group-1",
		ChannelType:       2,
		MessageScopedUIDs: []string{"u1"},
	})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("SubmitCommitted() error = %v, want %v", err, ErrRouteNotReady)
	}
	if len(local.requests) != 0 {
		t.Fatalf("local requests = %d, want 0", len(local.requests))
	}
	if len(remote.requests) != 0 {
		t.Fatalf("remote requests = %d, want 0", len(remote.requests))
	}
}

func TestDispatcherSplitsTargetBatchSize(t *testing.T) {
	local := &recordingLocalProcessor{}
	dispatcher := NewDispatcher(DispatcherOptions{
		LocalNodeID: 1,
		Resolver: &mappingRecipientAuthorityResolver{
			targets: map[string]authority.Target{
				"u1": localTarget(1),
				"u2": localTarget(1),
				"u3": localTarget(1),
			},
		},
		Local:           local,
		TargetBatchSize: 2,
	})

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:         "group-1",
		ChannelType:       2,
		MessageScopedUIDs: []string{"u1", "u2", "u3"},
	})
	if err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if len(local.requests) != 2 {
		t.Fatalf("local requests = %d, want 2", len(local.requests))
	}
	if got := len(local.requests[0].Recipients); got != 2 {
		t.Fatalf("first batch size = %d, want 2", got)
	}
	if got := len(local.requests[1].Recipients); got != 1 {
		t.Fatalf("second batch size = %d, want 1", got)
	}
}

func TestDispatcherChecksContextBetweenTargetChunks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	local := &recordingLocalProcessor{
		onProcess: func(ProcessRequest) {
			cancel()
		},
	}
	dispatcher := NewDispatcher(DispatcherOptions{
		LocalNodeID: 1,
		Resolver: &mappingRecipientAuthorityResolver{
			targets: map[string]authority.Target{
				"u1": localTarget(1),
				"u2": localTarget(1),
			},
		},
		Local:           local,
		TargetBatchSize: 1,
	})

	err := dispatcher.SubmitCommitted(ctx, messageevents.MessageCommitted{
		ChannelID:         "group-1",
		ChannelType:       2,
		MessageScopedUIDs: []string{"u1", "u2"},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SubmitCommitted() error = %v, want %v", err, context.Canceled)
	}
	if len(local.requests) != 1 {
		t.Fatalf("local requests = %d, want 1", len(local.requests))
	}
	wantRecipients := []Recipient{{UID: "u1"}}
	if !reflect.DeepEqual(local.requests[0].Recipients, wantRecipients) {
		t.Fatalf("first request recipients = %#v, want %#v", local.requests[0].Recipients, wantRecipients)
	}
}

func TestDispatcherRejectsStuckCursor(t *testing.T) {
	tests := []struct {
		name  string
		pages []RecipientPage
	}{
		{
			name: "empty cursor",
			pages: []RecipientPage{
				{Recipients: []Recipient{{UID: ""}}},
			},
		},
		{
			name: "unchanged cursor",
			pages: []RecipientPage{
				{Cursor: "same"},
				{Cursor: "same"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dispatcher := NewDispatcher(DispatcherOptions{
				LocalNodeID: 1,
				Recipients: &recordingRecipientSource{
					pages: tt.pages,
				},
			})

			err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
				ChannelID:   "group-1",
				ChannelType: 2,
			})
			if !errors.Is(err, ErrRouteNotReady) {
				t.Fatalf("SubmitCommitted() error = %v, want %v", err, ErrRouteNotReady)
			}
		})
	}
}

func TestDispatcherRejectsStuckCursorBeforeDispatchingNonFinalPage(t *testing.T) {
	local := &recordingLocalProcessor{}
	remote := &recordingRecipientRemote{}
	dispatcher := NewDispatcher(DispatcherOptions{
		LocalNodeID: 1,
		Recipients: &recordingRecipientSource{
			pages: []RecipientPage{
				{Recipients: []Recipient{{UID: "u1"}}},
			},
		},
		Resolver: &mappingRecipientAuthorityResolver{
			targets: map[string]authority.Target{
				"u1": localTarget(1),
			},
		},
		Local:  local,
		Remote: remote,
	})

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:   "group-1",
		ChannelType: 2,
	})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("SubmitCommitted() error = %v, want %v", err, ErrRouteNotReady)
	}
	if len(local.requests) != 0 {
		t.Fatalf("local requests = %d, want 0", len(local.requests))
	}
	if len(remote.requests) != 0 {
		t.Fatalf("remote requests = %d, want 0", len(remote.requests))
	}
}

func TestDispatcherDispatchesFinalPageWithEmptyCursor(t *testing.T) {
	local := &recordingLocalProcessor{}
	dispatcher := NewDispatcher(DispatcherOptions{
		LocalNodeID: 1,
		Recipients: &recordingRecipientSource{
			pages: []RecipientPage{
				{
					Recipients: []Recipient{{UID: "u1"}},
					Done:       true,
				},
			},
		},
		Resolver: &mappingRecipientAuthorityResolver{
			targets: map[string]authority.Target{
				"u1": localTarget(1),
			},
		},
		Local: local,
	})

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:   "group-1",
		ChannelType: 2,
	})
	if err != nil {
		t.Fatalf("SubmitCommitted() error = %v", err)
	}
	if len(local.requests) != 1 {
		t.Fatalf("local requests = %d, want 1", len(local.requests))
	}
}

func TestDispatcherSkipsEmptyRecipients(t *testing.T) {
	t.Run("scoped", func(t *testing.T) {
		local := &recordingLocalProcessor{}
		remote := &recordingRecipientRemote{}
		dispatcher := NewDispatcher(DispatcherOptions{
			LocalNodeID: 1,
			Local:       local,
			Remote:      remote,
		})

		err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
			ChannelID:         "group-1",
			ChannelType:       2,
			MessageScopedUIDs: []string{"", ""},
		})
		if err != nil {
			t.Fatalf("SubmitCommitted() error = %v", err)
		}
		if len(local.requests) != 0 {
			t.Fatalf("local requests = %d, want 0", len(local.requests))
		}
		if len(remote.requests) != 0 {
			t.Fatalf("remote requests = %d, want 0", len(remote.requests))
		}
	})

	t.Run("paged", func(t *testing.T) {
		local := &recordingLocalProcessor{}
		remote := &recordingRecipientRemote{}
		dispatcher := NewDispatcher(DispatcherOptions{
			LocalNodeID: 1,
			Recipients: &recordingRecipientSource{
				pages: []RecipientPage{
					{
						Recipients: []Recipient{{UID: ""}},
						Done:       true,
					},
				},
			},
			Local:  local,
			Remote: remote,
		})

		err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
			ChannelID:   "group-1",
			ChannelType: 2,
		})
		if err != nil {
			t.Fatalf("SubmitCommitted() error = %v", err)
		}
		if len(local.requests) != 0 {
			t.Fatalf("local requests = %d, want 0", len(local.requests))
		}
		if len(remote.requests) != 0 {
			t.Fatalf("remote requests = %d, want 0", len(remote.requests))
		}
	})
}

func TestDispatcherRequiresLocalProcessorForLocalTarget(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherOptions{
		LocalNodeID: 1,
		Resolver: &mappingRecipientAuthorityResolver{
			targets: map[string]authority.Target{
				"u1": localTarget(1),
			},
		},
		Remote: &recordingRecipientRemote{},
	})

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:         "group-1",
		ChannelType:       2,
		MessageScopedUIDs: []string{"u1"},
	})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("SubmitCommitted() error = %v, want %v", err, ErrRouteNotReady)
	}
}

func TestDispatcherRequiresRemoteForRemoteTarget(t *testing.T) {
	dispatcher := NewDispatcher(DispatcherOptions{
		LocalNodeID: 1,
		Resolver: &mappingRecipientAuthorityResolver{
			targets: map[string]authority.Target{
				"u1": localTarget(2),
			},
		},
		Local: &recordingLocalProcessor{},
	})

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:         "group-1",
		ChannelType:       2,
		MessageScopedUIDs: []string{"u1"},
	})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("SubmitCommitted() error = %v, want %v", err, ErrRouteNotReady)
	}
}

func TestNilDispatcherReturnsRouteNotReady(t *testing.T) {
	var dispatcher *Dispatcher

	err := dispatcher.SubmitCommitted(context.Background(), messageevents.MessageCommitted{
		ChannelID:         "group-1",
		ChannelType:       2,
		MessageScopedUIDs: []string{"u1"},
	})
	if !errors.Is(err, ErrRouteNotReady) {
		t.Fatalf("SubmitCommitted() error = %v, want %v", err, ErrRouteNotReady)
	}
}

type recordingRecipientSource struct {
	calls  []recipientSourceCall
	pages  []RecipientPage
	err    error
	onCall func(string)
}

func (s *recordingRecipientSource) NextPage(_ context.Context, event messageevents.MessageCommitted, cursor string, limit int) (RecipientPage, error) {
	if s.onCall != nil {
		s.onCall(cursor)
	}
	s.calls = append(s.calls, recipientSourceCall{
		event:  event.Clone(),
		cursor: cursor,
		limit:  limit,
	})
	if s.err != nil {
		return RecipientPage{}, s.err
	}
	if len(s.pages) == 0 {
		return RecipientPage{Done: true}, nil
	}
	page := s.pages[0]
	s.pages = s.pages[1:]
	page.Recipients = append([]Recipient(nil), page.Recipients...)
	return page, nil
}

type recipientSourceCall struct {
	event  messageevents.MessageCommitted
	cursor string
	limit  int
}

type panicRecipientSource struct {
	t *testing.T
}

func (s *panicRecipientSource) NextPage(context.Context, messageevents.MessageCommitted, string, int) (RecipientPage, error) {
	s.t.Fatal("RecipientSource called for scoped committed event")
	return RecipientPage{}, nil
}

type mappingRecipientAuthorityResolver struct {
	targets map[string]authority.Target
	err     error
}

func (r *mappingRecipientAuthorityResolver) ResolveRecipientAuthority(_ context.Context, uid string) (authority.Target, error) {
	if r.err != nil {
		return authority.Target{}, r.err
	}
	return r.targets[uid], nil
}

type recordingLocalProcessor struct {
	requests  []ProcessRequest
	err       error
	onProcess func(ProcessRequest)
}

func (p *recordingLocalProcessor) Process(_ context.Context, req ProcessRequest) error {
	req = cloneProcessRequest(req)
	p.requests = append(p.requests, req)
	if p.onProcess != nil {
		p.onProcess(req)
	}
	return p.err
}

type recordingRecipientRemote struct {
	requests []ProcessRequest
	err      error
}

func (r *recordingRecipientRemote) ProcessRemote(_ context.Context, req ProcessRequest) error {
	r.requests = append(r.requests, cloneProcessRequest(req))
	return r.err
}

func cloneProcessRequest(req ProcessRequest) ProcessRequest {
	req.Event = req.Event.Clone()
	req.Recipients = append([]Recipient(nil), req.Recipients...)
	return req
}
