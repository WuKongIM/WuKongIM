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
	recovery  *presenceusecase.AuthorityRecovery
}

var _ accessnode.PresenceAuthority = (*PresenceDirectoryAuthority)(nil)
var _ accessnode.PresenceBatchAuthority = (*PresenceDirectoryAuthority)(nil)
var _ accessnode.PresenceTargetBatchAuthority = (*PresenceDirectoryAuthority)(nil)

// NewPresenceDirectoryAuthority creates an authority adapter for directory.
func NewPresenceDirectoryAuthority(directory *authoritypresence.Directory) *PresenceDirectoryAuthority {
	return &PresenceDirectoryAuthority{directory: directory}
}

// SetRecovery installs bounded UID reconstruction before serving production traffic.
func (a *PresenceDirectoryAuthority) SetRecovery(r *presenceusecase.AuthorityRecovery) {
	a.recovery = r
}

// RegisterRoute reserves one exact route in the local authority directory.
func (a *PresenceDirectoryAuthority) RegisterRoute(ctx context.Context, target presenceusecase.RouteTarget, route presenceusecase.Route) (presenceusecase.RegisterResult, error) {
	if err := contextError(ctx); err != nil {
		return presenceusecase.RegisterResult{}, presenceusecase.WrapActivationPhase(presenceusecase.ActivationPhaseDirectoryRegister, err)
	}
	if a == nil || a.directory == nil {
		return presenceusecase.RegisterResult{}, presenceusecase.WrapActivationPhase(presenceusecase.ActivationPhaseDirectoryRegister, authoritypresence.ErrRouteNotReady)
	}
	if a.recovery != nil {
		if _, err := a.recovery.Endpoints(ctx, target, []string{route.UID}); err != nil {
			return presenceusecase.RegisterResult{}, presenceusecase.WrapActivationPhase(presenceusecase.ActivationPhaseRecovery, err)
		}
	}
	result, err := a.directory.RegisterRoute(target, route)
	if err != nil {
		return result, presenceusecase.WrapActivationPhase(presenceusecase.ActivationPhaseDirectoryRegister, err)
	}
	return result, nil
}

// CommitRoute commits one exact pending route token.
func (a *PresenceDirectoryAuthority) CommitRoute(ctx context.Context, target presenceusecase.RouteTarget, token string) error {
	if err := contextError(ctx); err != nil {
		return presenceusecase.WrapActivationPhase(presenceusecase.ActivationPhaseDirectoryCommit, err)
	}
	if a == nil || a.directory == nil {
		return presenceusecase.WrapActivationPhase(presenceusecase.ActivationPhaseDirectoryCommit, authoritypresence.ErrRouteNotReady)
	}
	if err := a.directory.CommitRoute(target, presenceusecase.PendingRouteToken(token)); err != nil {
		return presenceusecase.WrapActivationPhase(presenceusecase.ActivationPhaseDirectoryCommit, err)
	}
	return nil
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
	if a.recovery != nil {
		return a.recovery.Endpoints(ctx, target, []string{uid})
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
	if a.recovery != nil {
		return a.recovery.Endpoints(ctx, target, uids)
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
	if a.recovery != nil {
		return a.recovery.EndpointsByTargets(ctx, groups)
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
