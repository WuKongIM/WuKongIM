package cluster

import (
	"context"
	"errors"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"strings"
	"testing"
)

func TestPresenceDirectoryActivationFailurePhasesPreserveCause(t *testing.T) {
	target := presence.RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, LeaderTerm: 1}
	route := presence.Route{UID: "private-uid", OwnerNodeID: 1, OwnerBootID: 1, OwnerSeq: 1, SessionID: 1}
	for _, test := range []struct {
		name, phase string
		run         func() error
		cause       error
	}{
		{"missing register directory", "presence activation directory register", func() error {
			_, err := NewPresenceDirectoryAuthority(nil).RegisterRoute(context.Background(), target, route)
			return err
		}, authority.ErrRouteNotReady},
		{"stale register target", "presence activation directory register", func() error {
			d := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1})
			_, err := NewPresenceDirectoryAuthority(d).RegisterRoute(context.Background(), target, route)
			return err
		}, authority.ErrNotLeader},
		{"recovery", "presence activation recovery", func() error {
			d := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
			d.BecomeAuthority(target)
			a := NewPresenceDirectoryAuthority(d)
			r := presence.NewAuthorityRecovery(d, activationPhaseRecoveryOwners{})
			defer r.Close(context.Background())
			a.SetRecovery(r)
			_, err := a.RegisterRoute(context.Background(), target, route)
			return err
		}, authority.ErrRouteNotReady},
		{"missing commit directory", "presence activation directory commit", func() error {
			return NewPresenceDirectoryAuthority(nil).CommitRoute(context.Background(), target, "private-token")
		}, authority.ErrRouteNotReady},
		{"commit", "presence activation directory commit", func() error {
			d := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1})
			d.BecomeAuthority(target)
			return NewPresenceDirectoryAuthority(d).CommitRoute(context.Background(), target, "private-token")
		}, authority.ErrRouteNotReady},
		{"cancelled register", "presence activation directory register", func() error {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := NewPresenceDirectoryAuthority(nil).RegisterRoute(ctx, target, route)
			return err
		}, context.Canceled},
		{"cancelled commit", "presence activation directory commit", func() error {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return NewPresenceDirectoryAuthority(nil).CommitRoute(ctx, target, "private-token")
		}, context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if !errors.Is(err, test.cause) {
				t.Fatalf("cause = %v, want %v", err, test.cause)
			}
			if !strings.HasPrefix(err.Error(), test.phase+": ") {
				t.Fatalf("phase = %q, want %q", err.Error(), test.phase)
			}
			if strings.Contains(err.Error(), "private-") {
				t.Fatalf("identity/token added: %q", err.Error())
			}
		})
	}
}

type activationPhaseRecoveryOwners struct{}

func (activationPhaseRecoveryOwners) RecoverRoutes(context.Context, presence.RouteTarget, []string) ([]presence.Route, error) {
	return nil, authority.ErrRouteNotReady
}
