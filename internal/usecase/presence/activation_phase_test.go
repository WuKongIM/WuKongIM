package presence

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestActivateFailurePhasesPreserveCauseAndBoundedText(t *testing.T) {
	for _, test := range []struct {
		name, phase string
		mutate      func(*Options, *fakeLocalRegistry, *activationPhaseAuthority)
		cause       error
	}{
		{"target", "presence activation target", func(o *Options, _ *fakeLocalRegistry, _ *activationPhaseAuthority) {
			o.HashSlot = func(string) (uint16, error) { return 0, errBoom }
		}, errBoom},
		{"local register", "presence activation local register", func(o *Options, l *fakeLocalRegistry, _ *activationPhaseAuthority) {
			o.Local = activationPhaseLocal{l, errBoom}
		}, errBoom},
		{"register", "presence activation register", func(_ *Options, _ *fakeLocalRegistry, a *activationPhaseAuthority) { a.registerErr = errBoom }, errBoom},
		{"action", "presence activation action", func(o *Options, _ *fakeLocalRegistry, a *activationPhaseAuthority) {
			a.registerResult = RegisterResult{PendingToken: "private-token", Actions: []RouteAction{{UID: "private-uid"}}}
			o.OwnerActions = activationPhaseActions{errBoom}
		}, errBoom},
		{"commit", "presence activation commit", func(_ *Options, _ *fakeLocalRegistry, a *activationPhaseAuthority) {
			a.registerResult = RegisterResult{PendingToken: "private-token"}
			a.err = errBoom
		}, errBoom},
		{"local recheck", "presence activation local recheck", func(_ *Options, l *fakeLocalRegistry, _ *activationPhaseAuthority) {
			l.markActiveErr = ErrSessionNotActive
		}, ErrSessionNotActive},
		{"cancelled register", "presence activation register", func(_ *Options, _ *fakeLocalRegistry, a *activationPhaseAuthority) { a.registerErr = context.Canceled }, context.Canceled},
		{"deadline register", "presence activation register", func(_ *Options, _ *fakeLocalRegistry, a *activationPhaseAuthority) {
			a.registerErr = context.DeadlineExceeded
		}, context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := []string{}
			local := newFakeLocalRegistry(&calls)
			authority := &activationPhaseAuthority{fakeAuthorityClient: &fakeAuthorityClient{calls: &calls}}
			opts := Options{Local: local, Authority: authority}
			test.mutate(&opts, local, authority)
			err := New(opts).Activate(context.Background(), ActivateCommand{UID: "private-uid", DeviceID: "private-device", SessionID: 11, Session: fakeSessionHandle{}})
			if !errors.Is(err, test.cause) {
				t.Fatalf("cause = %v, want %v", err, test.cause)
			}
			if !strings.HasPrefix(err.Error(), test.phase+": ") {
				t.Fatalf("phase = %q, want %q", err.Error(), test.phase)
			}
			for _, secret := range []string{"private-uid", "private-device", "private-token"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("identity/token added: %q", err.Error())
				}
			}
		})
	}
}

type activationPhaseAuthority struct {
	*fakeAuthorityClient
	err error
}

func (a *activationPhaseAuthority) CommitRoute(context.Context, PendingRouteToken) error {
	*a.calls = append(*a.calls, "authority.commit")
	return a.err
}

type activationPhaseLocal struct {
	*fakeLocalRegistry
	err error
}

func (l activationPhaseLocal) RegisterPending(LocalSession) error {
	*l.calls = append(*l.calls, "local.register_pending")
	return l.err
}

type activationPhaseActions struct{ err error }

func (a activationPhaseActions) ApplyRouteAction(context.Context, RouteAction) error { return a.err }
