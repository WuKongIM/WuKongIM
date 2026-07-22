package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	authoritypresence "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	presenceusecase "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// PresenceDirectoryAuthority adapts the node-local authoritative presence
// directory to the entry-agnostic presence authority contract.
type PresenceDirectoryAuthority struct {
	directory *authoritypresence.Directory
}

var _ accessnode.PresenceAuthority = (*PresenceDirectoryAuthority)(nil)
var _ accessnode.PresenceBatchAuthority = (*PresenceDirectoryAuthority)(nil)
var _ accessnode.PresenceTargetBatchAuthority = (*PresenceDirectoryAuthority)(nil)

// NewPresenceDirectoryAuthority creates an authority adapter for directory.
func NewPresenceDirectoryAuthority(directory *authoritypresence.Directory) *PresenceDirectoryAuthority {
	return &PresenceDirectoryAuthority{directory: directory}
}

// RegisterRoute reserves one exact route in the local authority directory.
func (a *PresenceDirectoryAuthority) RegisterRoute(ctx context.Context, target presenceusecase.RouteTarget, route presenceusecase.Route) (presenceusecase.RegisterResult, error) {
	if err := contextError(ctx); err != nil {
		return presenceusecase.RegisterResult{}, err
	}
	if a == nil || a.directory == nil {
		return presenceusecase.RegisterResult{}, authoritypresence.ErrRouteNotReady
	}
	return a.directory.RegisterRoute(target, route)
}

// CommitRoute commits one exact pending route token.
func (a *PresenceDirectoryAuthority) CommitRoute(ctx context.Context, target presenceusecase.RouteTarget, token string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if a == nil || a.directory == nil {
		return authoritypresence.ErrRouteNotReady
	}
	return a.directory.CommitRoute(target, presenceusecase.PendingRouteToken(token))
}

// AbortRoute aborts one exact pending route token.
func (a *PresenceDirectoryAuthority) AbortRoute(ctx context.Context, target presenceusecase.RouteTarget, token string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if a == nil || a.directory == nil {
		return authoritypresence.ErrRouteNotReady
	}
	return a.directory.AbortRoute(target, presenceusecase.PendingRouteToken(token))
}

// UnregisterRoute removes one exact owner-fenced route.
func (a *PresenceDirectoryAuthority) UnregisterRoute(ctx context.Context, target presenceusecase.RouteTarget, identity presenceusecase.RouteIdentity, ownerSeq uint64) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if a == nil || a.directory == nil {
		return authoritypresence.ErrRouteNotReady
	}
	return a.directory.UnregisterRoute(target, identity, ownerSeq)
}

// EndpointsByUID resolves one UID under one exact authority target.
func (a *PresenceDirectoryAuthority) EndpointsByUID(ctx context.Context, target presenceusecase.RouteTarget, uid string) ([]presenceusecase.Route, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if a == nil || a.directory == nil {
		return nil, authoritypresence.ErrRouteNotReady
	}
	return a.directory.EndpointsByUID(target, uid)
}

// EndpointsByUIDs resolves one UID group under one exact authority target.
func (a *PresenceDirectoryAuthority) EndpointsByUIDs(ctx context.Context, target presenceusecase.RouteTarget, uids []string) ([]presenceusecase.Route, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if a == nil || a.directory == nil {
		return nil, authoritypresence.ErrRouteNotReady
	}
	return a.directory.EndpointsByUIDs(target, uids)
}

// EndpointsByTargets resolves aligned target groups through one directory batch.
func (a *PresenceDirectoryAuthority) EndpointsByTargets(ctx context.Context, groups []presenceusecase.EndpointLookupGroup) []presenceusecase.EndpointLookupResult {
	if err := contextError(ctx); err != nil {
		return presenceDirectoryErrorResults(len(groups), err)
	}
	if a == nil || a.directory == nil {
		return presenceDirectoryErrorResults(len(groups), authoritypresence.ErrRouteNotReady)
	}
	return a.directory.EndpointsByTargets(groups)
}

// TouchRoutes refreshes owner-observed activity for exact routes.
func (a *PresenceDirectoryAuthority) TouchRoutes(ctx context.Context, target presenceusecase.RouteTarget, routes []presenceusecase.Route) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if a == nil || a.directory == nil {
		return authoritypresence.ErrRouteNotReady
	}
	return a.directory.TouchRoutes(target, routes)
}

func presenceDirectoryErrorResults(count int, err error) []presenceusecase.EndpointLookupResult {
	results := make([]presenceusecase.EndpointLookupResult, count)
	for i := range results {
		results[i].Err = err
	}
	return results
}
