package management

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
)

var (
	// ErrControllerVoterPromotionUnavailable reports that Controller voter promotion dependencies are unavailable.
	ErrControllerVoterPromotionUnavailable = errors.New("internalv2/usecase/management: controller voter promotion unavailable")
	// ErrControllerVoterPromotionBlocked reports that Controller voter promotion failed a safety gate.
	ErrControllerVoterPromotionBlocked = errors.New("internalv2/usecase/management: controller voter promotion blocked")
)

// PromoteControllerVoterRequest is the manager-facing Controller voter promotion intent.
type PromoteControllerVoterRequest struct {
	// NodeID is the active data node to promote into Controller voting membership.
	NodeID uint64
	// ExpectedRevision fences the request to the operator-observed control snapshot revision.
	ExpectedRevision uint64
}

// PromoteControllerVoterResponse describes the promotion result and voter sets.
type PromoteControllerVoterResponse struct {
	// Changed reports whether durable ControllerV2 state changed.
	Changed bool
	// NodeID is the promoted or already-voting node identity.
	NodeID uint64
	// StateRevision is the resulting or observed control-state revision.
	StateRevision uint64
	// PreviousVoters is the durable Controller voter set before promotion.
	PreviousVoters []uint64
	// NextVoters is the durable Controller voter set after promotion.
	NextVoters []uint64
	// Warnings contains bounded operator warnings from the control writer.
	Warnings []string
}

// ControllerVoterReadiness is the target-node readiness view for promotion.
type ControllerVoterReadiness struct {
	// NodeID is the node identity that reported readiness.
	NodeID uint64
	// ClusterID is the target node's mirrored ControllerV2 cluster identity when known.
	ClusterID string
	// Reachable reports whether the readiness RPC reached the target node.
	Reachable bool
	// TransportReady reports whether node transport is serving.
	TransportReady bool
	// ControlReady reports whether the target node has a usable control mirror.
	ControlReady bool
	// RuntimeReady reports whether local app runtimes required for promotion are ready.
	RuntimeReady bool
	// CanPrepare reports whether the target can enter Controller voter preparation.
	CanPrepare bool
	// MirrorRevision is the latest control-state revision observed by the target node.
	MirrorRevision uint64
	// IsVoter reports whether the target currently observes itself in the Controller Raft voter set.
	IsVoter bool
	// ControlLeaderID is the Controller Raft leader observed by the target when known.
	ControlLeaderID uint64
	// ConfigIndex is the target's observed Controller Raft applied config index.
	ConfigIndex uint64
	// Voters is the Controller Raft voter set observed by the target.
	Voters []uint64
	// Unknown reports that readiness could not be determined.
	Unknown bool
	// LastError carries a compact diagnostic for failed readiness checks.
	LastError string
}

// ControllerVoterEndpoint identifies a Controller voter endpoint used for live preparation.
type ControllerVoterEndpoint struct {
	// NodeID is the Controller voter node identity.
	NodeID uint64
	// Addr is the stable cluster control-plane address for the voter.
	Addr string
}

// PrepareControllerVoterRequest asks the target node to prepare local Controller Raft runtime.
type PrepareControllerVoterRequest struct {
	// NodeID is the target node being prepared.
	NodeID uint64
	// ClusterID is the stable ControllerV2 cluster identity expected by the target.
	ClusterID string
	// ExpectedRevision is the minimum mirrored control revision required before preparing.
	ExpectedRevision uint64
	// NextVoters is the complete Controller voter endpoint set after promotion.
	NextVoters []ControllerVoterEndpoint
}

// PrepareControllerVoterResponse reports target-side preparation and best-effort local Controller Raft status.
type PrepareControllerVoterResponse struct {
	// NodeID is the node that handled preparation.
	NodeID uint64
	// Prepared reports whether the target is ready to receive Controller Raft traffic.
	Prepared bool
	// StateRevision is the mirrored control-state revision preserved before Raft startup.
	StateRevision uint64
	// ObservedConfigIndex is the local Controller Raft applied config index when already visible.
	ObservedConfigIndex uint64
	// ObservedVoters is the local Controller Raft voter set when already visible.
	ObservedVoters []uint64
}

type controllerVoterPromotionBlockedError struct {
	reason string
}

func (e *controllerVoterPromotionBlockedError) Error() string {
	return fmt.Sprintf("%s: %s", ErrControllerVoterPromotionBlocked, e.reason)
}

func (e *controllerVoterPromotionBlockedError) Unwrap() error {
	return ErrControllerVoterPromotionBlocked
}

// PromoteControllerVoter validates and submits an online Controller voter promotion.
func (a *App) PromoteControllerVoter(ctx context.Context, req PromoteControllerVoterRequest) (resp PromoteControllerVoterResponse, err error) {
	if a != nil {
		defer func() {
			a.observeControllerVoterPromotionAttempt(resp, err)
		}()
	}
	if err := ctxErr(ctx); err != nil {
		return PromoteControllerVoterResponse{}, err
	}
	if req.NodeID == 0 {
		return PromoteControllerVoterResponse{}, controllerVoterPromotionBlocked("invalid_node_id")
	}
	if a == nil || a.cluster == nil {
		return PromoteControllerVoterResponse{}, ErrControllerVoterPromotionUnavailable
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return PromoteControllerVoterResponse{}, err
	}
	if req.ExpectedRevision != 0 && req.ExpectedRevision != snapshot.Revision {
		return PromoteControllerVoterResponse{}, controllerVoterPromotionBlocked("expected_revision_mismatch")
	}
	target, ok := findControlNode(snapshot, req.NodeID)
	if !ok {
		return PromoteControllerVoterResponse{}, ErrNodeLifecycleNotFound
	}
	previousVoters := durableControllerVoterIDs(snapshot)
	if hasRole(target.Roles, control.RoleController) {
		return PromoteControllerVoterResponse{
			Changed:        false,
			NodeID:         req.NodeID,
			StateRevision:  snapshot.Revision,
			PreviousVoters: append([]uint64(nil), previousVoters...),
			NextVoters:     append([]uint64(nil), previousVoters...),
		}, nil
	}
	if err := validateControllerVoterPromotionTarget(snapshot, target); err != nil {
		return PromoteControllerVoterResponse{}, err
	}
	if len(previousVoters) == 0 {
		return PromoteControllerVoterResponse{}, controllerVoterPromotionBlocked("controller_voter_set_empty")
	}
	endpoints, err := nextControllerVoterEndpoints(snapshot, target)
	if err != nil {
		return PromoteControllerVoterResponse{}, err
	}
	if a.controllerVoterPromoter == nil || a.controllerVoterReadiness == nil || a.controllerVoterPreparer == nil {
		return PromoteControllerVoterResponse{}, ErrControllerVoterPromotionUnavailable
	}
	phaseStarted := time.Now()
	readiness, err := a.controllerVoterReadiness.ControllerVoterReadiness(ctx, req.NodeID)
	a.observeControllerVoterPromotionPhase("readiness", phaseStarted)
	if err != nil {
		return PromoteControllerVoterResponse{}, mapControllerVoterPromotionReadinessError(err)
	}
	if err := validateControllerVoterReadiness(snapshot, req.NodeID, readiness); err != nil {
		return PromoteControllerVoterResponse{}, err
	}
	phaseStarted = time.Now()
	prep, err := a.controllerVoterPreparer.PrepareControllerVoter(ctx, PrepareControllerVoterRequest{
		NodeID:           req.NodeID,
		ClusterID:        snapshot.ClusterID,
		ExpectedRevision: snapshot.Revision,
		NextVoters:       cloneControllerVoterEndpoints(endpoints),
	})
	a.observeControllerVoterPromotionPhase("prepare", phaseStarted)
	if err != nil {
		return PromoteControllerVoterResponse{}, mapControllerVoterPromotionPrepareError(err)
	}
	if err := validatePrepareControllerVoterPrepared(snapshot, req.NodeID, prep); err != nil {
		return PromoteControllerVoterResponse{}, err
	}
	phaseStarted = time.Now()
	result, err := a.controllerVoterPromoter.PromoteControllerVoter(ctx, control.PromoteControllerVoterRequest{
		NodeID:           req.NodeID,
		ExpectedRevision: snapshot.Revision,
		ExpectedVoters:   append([]uint64(nil), previousVoters...),
	})
	a.observeControllerVoterPromotionPhase("commit_state", phaseStarted)
	if err != nil {
		return PromoteControllerVoterResponse{}, mapControllerVoterPromotionWriteError(err)
	}
	nodeID := result.Node.NodeID
	if nodeID == 0 {
		nodeID = req.NodeID
	}
	return PromoteControllerVoterResponse{
		Changed:        result.Changed,
		NodeID:         nodeID,
		StateRevision:  result.Revision,
		PreviousVoters: append([]uint64(nil), result.PreviousVoters...),
		NextVoters:     append([]uint64(nil), result.NextVoters...),
		Warnings:       append([]string(nil), result.Warnings...),
	}, nil
}

func validateControllerVoterPromotionTarget(snapshot control.Snapshot, target control.Node) error {
	switch {
	case target.JoinState != "" && target.JoinState != control.NodeJoinStateActive:
		return controllerVoterPromotionBlocked("target_not_active")
	case !hasRole(target.Roles, control.RoleData):
		return controllerVoterPromotionBlocked("target_not_data")
	case strings.TrimSpace(target.Addr) == "":
		return controllerVoterPromotionBlocked("target_address_missing")
	case target.Health.Freshness != control.NodeHealthFresh:
		return controllerVoterPromotionBlocked("target_health_stale")
	case target.Health.Status != control.NodeAlive:
		return controllerVoterPromotionBlocked("target_not_alive")
	case !target.Health.RuntimeReady:
		return controllerVoterPromotionBlocked("target_runtime_not_ready")
	case target.Health.ObservedControlRevision < snapshot.Revision:
		return controllerVoterPromotionBlocked("target_revision_stale")
	default:
		return nil
	}
}

func validateControllerVoterReadiness(snapshot control.Snapshot, expectedNodeID uint64, readiness ControllerVoterReadiness) error {
	readinessClusterID := strings.TrimSpace(readiness.ClusterID)
	switch {
	case readiness.Unknown:
		return controllerVoterPromotionBlocked("target_readiness_unknown")
	case readiness.NodeID != expectedNodeID:
		return controllerVoterPromotionBlocked("target_readiness_node_mismatch")
	case readinessClusterID == "" || readinessClusterID != strings.TrimSpace(snapshot.ClusterID):
		return controllerVoterPromotionBlocked("target_cluster_mismatch")
	case !readiness.Reachable || !readiness.TransportReady || !readiness.ControlReady || !readiness.RuntimeReady:
		return controllerVoterPromotionBlocked("target_readiness_not_ready")
	case !readiness.CanPrepare:
		return controllerVoterPromotionBlocked("target_prepare_unavailable")
	case readiness.MirrorRevision < snapshot.Revision:
		return controllerVoterPromotionBlocked("target_revision_stale")
	default:
		return nil
	}
}

func validatePrepareControllerVoterPrepared(snapshot control.Snapshot, expectedNodeID uint64, prep PrepareControllerVoterResponse) error {
	switch {
	case prep.NodeID != expectedNodeID:
		return controllerVoterPromotionBlocked("target_prepare_node_mismatch")
	case !prep.Prepared:
		return controllerVoterPromotionBlocked("target_prepare_not_ready")
	case prep.StateRevision < snapshot.Revision:
		return controllerVoterPromotionBlocked("target_prepare_revision_stale")
	default:
		return nil
	}
}

func durableControllerVoterIDs(snapshot control.Snapshot) []uint64 {
	ids := make([]uint64, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if hasRole(node.Roles, control.RoleController) {
			ids = append(ids, node.NodeID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func nextControllerVoterEndpoints(snapshot control.Snapshot, target control.Node) ([]ControllerVoterEndpoint, error) {
	byID := make(map[uint64]ControllerVoterEndpoint, len(snapshot.Nodes)+1)
	for _, node := range snapshot.Nodes {
		if !hasRole(node.Roles, control.RoleController) {
			continue
		}
		addr := strings.TrimSpace(node.Addr)
		if addr == "" {
			return nil, controllerVoterPromotionBlocked("controller_voter_address_missing")
		}
		byID[node.NodeID] = ControllerVoterEndpoint{NodeID: node.NodeID, Addr: addr}
	}
	byID[target.NodeID] = ControllerVoterEndpoint{NodeID: target.NodeID, Addr: strings.TrimSpace(target.Addr)}
	endpoints := make([]ControllerVoterEndpoint, 0, len(byID))
	for _, endpoint := range byID {
		endpoints = append(endpoints, endpoint)
	}
	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].NodeID < endpoints[j].NodeID })
	return endpoints, nil
}

func controllerVoterEndpointIDs(endpoints []ControllerVoterEndpoint) []uint64 {
	ids := make([]uint64, 0, len(endpoints))
	for _, endpoint := range endpoints {
		ids = append(ids, endpoint.NodeID)
	}
	return ids
}

func cloneControllerVoterEndpoints(in []ControllerVoterEndpoint) []ControllerVoterEndpoint {
	if len(in) == 0 {
		return nil
	}
	out := make([]ControllerVoterEndpoint, len(in))
	copy(out, in)
	return out
}

func controllerVoterPromotionBlocked(reason string) error {
	return &controllerVoterPromotionBlockedError{reason: reason}
}

func (a *App) observeControllerVoterPromotionAttempt(resp PromoteControllerVoterResponse, err error) {
	if a == nil || a.controllerVoterPromotion == nil {
		return
	}
	result := controllerVoterPromotionAttemptResult(resp, err)
	a.controllerVoterPromotion.ObserveControllerVoterPromotionAttempt(result)
	if result != "blocked" {
		return
	}
	reason := controllerVoterPromotionBlockerReason(err)
	if reason == "" {
		reason = "other"
	}
	a.controllerVoterPromotion.ObserveControllerVoterPromotionBlocker(reason)
}

func (a *App) observeControllerVoterPromotionPhase(phase string, started time.Time) {
	if a == nil || a.controllerVoterPromotion == nil {
		return
	}
	a.controllerVoterPromotion.ObserveControllerVoterPromotionPhase(phase, time.Since(started))
}

func controllerVoterPromotionAttemptResult(resp PromoteControllerVoterResponse, err error) string {
	if err == nil {
		if resp.Changed {
			return "changed"
		}
		return "noop"
	}
	if errors.Is(err, ErrControllerVoterPromotionUnavailable) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return "unavailable"
	}
	return "blocked"
}

func controllerVoterPromotionBlockerReason(err error) string {
	if err == nil {
		return ""
	}
	var blocked *controllerVoterPromotionBlockedError
	if errors.As(err, &blocked) {
		return blocked.reason
	}
	if cv2.IsExpectedRevisionMismatch(err) {
		return "expected_revision_mismatch"
	}
	msg := err.Error()
	for _, reason := range []string{"target_health_stale", "target_revision_stale", "expected_revision_mismatch"} {
		if strings.Contains(msg, reason) {
			return reason
		}
	}
	return ""
}

func mapControllerVoterPromotionReadinessError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrControllerVoterPromotionUnavailable, err)
}

func mapControllerVoterPromotionPrepareError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, ErrControllerVoterPromotionBlocked):
		return fmt.Errorf("%w: %w", ErrControllerVoterPromotionBlocked, err)
	case errors.Is(err, cv2.ErrNotLeader),
		errors.Is(err, cv2.ErrNotStarted),
		errors.Is(err, cv2.ErrStopped):
		return fmt.Errorf("%w: %w", ErrControllerVoterPromotionUnavailable, err)
	case cv2.IsExpectedRevisionMismatch(err), errors.Is(err, cv2.ErrProposalRejected):
		return fmt.Errorf("%w: %w", ErrControllerVoterPromotionBlocked, err)
	default:
		return fmt.Errorf("%w: %w", ErrControllerVoterPromotionUnavailable, err)
	}
}

func mapControllerVoterPromotionWriteError(err error) error {
	switch {
	case errors.Is(err, cv2.ErrNotLeader),
		errors.Is(err, cv2.ErrNotStarted),
		errors.Is(err, cv2.ErrStopped):
		return fmt.Errorf("%w: %w", ErrControllerVoterPromotionUnavailable, err)
	case errors.Is(err, cv2.ErrNodeLifecycleNotFound):
		return fmt.Errorf("%w: %w", ErrNodeLifecycleNotFound, err)
	case errors.Is(err, cv2.ErrNodeLifecycleConflict):
		return fmt.Errorf("%w: %w", ErrControllerVoterPromotionBlocked, err)
	case cv2.IsExpectedRevisionMismatch(err), errors.Is(err, cv2.ErrProposalRejected):
		return fmt.Errorf("%w: %w", ErrControllerVoterPromotionBlocked, err)
	default:
		return err
	}
}
