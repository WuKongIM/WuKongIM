package presence

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEndpointsByTargetsUsesTargetAwareBatchAuthority(t *testing.T) {
	target := RouteTarget{
		HashSlot:       7,
		SlotID:         11,
		LeaderNodeID:   2,
		LeaderTerm:     13,
		ConfigEpoch:    17,
		RouteRevision:  19,
		AuthorityEpoch: 23,
	}
	groups := []EndpointLookupGroup{{Target: target, UIDs: []string{"u1", "u2"}}}
	authority := &targetBatchLookupAuthority{
		results: []EndpointLookupResult{{Routes: []Route{{UID: "u2", SessionID: 29}}}},
	}
	app := New(Options{Authority: authority})

	results := app.EndpointsByTargets(context.Background(), groups)

	require.Equal(t, authority.results, results)
	require.Equal(t, groups, authority.groups)
	require.Zero(t, authority.legacyCalls)
}

func TestEndpointsByTargetsLegacyFallbackContinuesAfterGroupError(t *testing.T) {
	authority := &legacyLookupAuthority{
		routesByUID: map[string][]Route{
			"u1": {{UID: "u1", SessionID: 11}},
			"u3": {{UID: "u3", SessionID: 33}},
		},
		errsByUID: map[string]error{"u2": errLookupFailed},
	}
	app := New(Options{Authority: authority})
	groups := []EndpointLookupGroup{
		{Target: RouteTarget{LeaderNodeID: 2}, UIDs: []string{"u1", "u2", "not-reached"}},
		{Target: RouteTarget{LeaderNodeID: 3}, UIDs: []string{"u3"}},
	}

	results := app.EndpointsByTargets(context.Background(), groups)

	require.Len(t, results, 2)
	require.Equal(t, []Route{{UID: "u1", SessionID: 11}}, results[0].Routes)
	require.ErrorIs(t, results[0].Err, errLookupFailed)
	require.Equal(t, []Route{{UID: "u3", SessionID: 33}}, results[1].Routes)
	require.NoError(t, results[1].Err)
	require.Equal(t, []string{"u1", "u2", "u3"}, authority.legacyCalls)
}

func TestEndpointsByTargetsReportsUnavailableAuthorityPerGroup(t *testing.T) {
	app := New(Options{})

	results := app.EndpointsByTargets(nil, []EndpointLookupGroup{
		{UIDs: []string{"u1"}},
		{UIDs: []string{"u2"}},
	})

	require.Len(t, results, 2)
	require.ErrorIs(t, results[0].Err, ErrAuthorityUnavailable)
	require.ErrorIs(t, results[1].Err, ErrAuthorityUnavailable)
}

type legacyLookupAuthority struct {
	routesByUID map[string][]Route
	errsByUID   map[string]error
	legacyCalls []string
}

func (a *legacyLookupAuthority) RegisterRoute(context.Context, Route) (RegisterResult, error) {
	return RegisterResult{}, nil
}

func (a *legacyLookupAuthority) CommitRoute(context.Context, PendingRouteToken) error { return nil }

func (a *legacyLookupAuthority) AbortRoute(context.Context, PendingRouteToken) error { return nil }

func (a *legacyLookupAuthority) EnqueueUnregister(context.Context, RouteIdentity, uint64) {}

func (a *legacyLookupAuthority) EndpointsByUID(_ context.Context, uid string) ([]Route, error) {
	a.legacyCalls = append(a.legacyCalls, uid)
	return append([]Route(nil), a.routesByUID[uid]...), a.errsByUID[uid]
}

type targetBatchLookupAuthority struct {
	legacyLookupAuthority
	groups  []EndpointLookupGroup
	results []EndpointLookupResult
}

func (a *targetBatchLookupAuthority) EndpointsByTargets(_ context.Context, groups []EndpointLookupGroup) []EndpointLookupResult {
	a.groups = append([]EndpointLookupGroup(nil), groups...)
	return append([]EndpointLookupResult(nil), a.results...)
}

var errLookupFailed = errors.New("lookup failed")
