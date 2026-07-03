package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/controller/statefile"
)

const mirrorBeforeControllerVoterPromotionFile = "cluster-state.mirror-before-controller-voter-promotion.json"

type mirrorStateCandidate struct {
	path   string
	state  ClusterState
	exists bool
	valid  bool
	err    error
}

type mirrorStateSelection struct {
	active   mirrorStateCandidate
	backup   mirrorStateCandidate
	selected mirrorStateCandidate
}

// PromoteControllerVoter finalizes a Controller Raft voter promotion after live Raft proof.
func (r *Runtime) PromoteControllerVoter(ctx context.Context, req PromoteControllerVoterRequest) (PromoteControllerVoterResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return PromoteControllerVoterResult{}, err
	}
	if r == nil || r.raft == nil {
		return PromoteControllerVoterResult{}, ErrNotStarted
	}
	st, err := r.LocalState(ctx)
	if err != nil {
		return PromoteControllerVoterResult{}, err
	}
	node, ok := findLifecycleNode(st, req.NodeID)
	if !ok {
		return PromoteControllerVoterResult{}, fmt.Errorf("%w: node %d", ErrNodeLifecycleNotFound, req.NodeID)
	}
	addr := req.Addr
	if addr == "" {
		addr = node.Addr
	}
	expectedRevision := req.ExpectedRevision
	if expectedRevision == 0 {
		expectedRevision = st.Revision
	}
	previousVoters := controllerNodeIDsForRuntime(st.Controllers)
	if req.ExpectedRevision != 0 && req.ExpectedRevision != st.Revision {
		return PromoteControllerVoterResult{}, fmt.Errorf("%w: %w: %s", ErrProposalRejected, ErrExpectedRevisionMismatch, fsm.ReasonExpectedRevisionMismatch)
	}
	if req.ExpectedVoters != nil && !sameRuntimeUint64Set(req.ExpectedVoters, previousVoters) {
		return PromoteControllerVoterResult{}, fmt.Errorf("%w: %s", ErrProposalRejected, fsm.ReasonControllerVoterSetMismatch)
	}
	observedConfigIndex := req.ObservedConfigIndex
	observedVoters := append([]uint64(nil), req.ObservedVoters...)
	if observedConfigIndex == 0 || len(observedVoters) == 0 {
		observedConfigIndex, observedVoters, err = r.ensureControllerRaftVoter(ctx, req.NodeID)
		if err != nil {
			return PromoteControllerVoterResult{}, err
		}
	}
	proposal, err := r.raft.ProposeResult(ctx, command.Command{
		Kind:             command.KindPromoteControllerVoter,
		IssuedAt:         r.cfg.Now().UTC(),
		ExpectedRevision: &expectedRevision,
		ControllerVoterPromotion: &command.ControllerVoterPromotion{
			TargetNodeID:           req.NodeID,
			TargetAddr:             addr,
			ExpectedPreviousVoters: copyOptionalUint64s(req.ExpectedVoters),
			ObservedConfigIndex:    observedConfigIndex,
			ObservedVoters:         observedVoters,
		},
	})
	if err != nil {
		return PromoteControllerVoterResult{}, err
	}
	if err := r.publishFromState(ctx); err != nil {
		return PromoteControllerVoterResult{}, err
	}
	updated, err := r.LocalState(ctx)
	if err != nil {
		return PromoteControllerVoterResult{}, err
	}
	finalNode, ok := findLifecycleNode(updated, req.NodeID)
	if !ok {
		return PromoteControllerVoterResult{}, fmt.Errorf("controllerv2: node %d not found after controller voter promotion", req.NodeID)
	}
	return PromoteControllerVoterResult{
		Changed:        proposal.Changed,
		Node:           finalNode,
		Revision:       updated.Revision,
		PreviousVoters: previousVoters,
		NextVoters:     controllerNodeIDsForRuntime(updated.Controllers),
	}, nil
}

func (r *Runtime) ensureControllerRaftVoter(ctx context.Context, nodeID uint64) (uint64, []uint64, error) {
	status := r.raft.Status()
	if containsRuntimeUint64(status.Voters, nodeID) {
		return controllerRaftStatusProofIndex(status), cloneSortedUint64s(status.Voters), nil
	}
	if !containsRuntimeUint64(status.Learners, nodeID) {
		if _, err := r.raft.AddLearner(ctx, nodeID); err != nil {
			status = r.raft.Status()
			if containsRuntimeUint64(status.Voters, nodeID) {
				return controllerRaftStatusProofIndex(status), cloneSortedUint64s(status.Voters), nil
			}
			if !containsRuntimeUint64(status.Learners, nodeID) {
				return 0, nil, err
			}
		}
	}
	membership, err := r.raft.PromoteLearner(ctx, nodeID)
	if err != nil {
		status = r.raft.Status()
		if containsRuntimeUint64(status.Voters, nodeID) {
			return controllerRaftStatusProofIndex(status), cloneSortedUint64s(status.Voters), nil
		}
		return 0, nil, err
	}
	return membership.Index, cloneSortedUint64s(membership.ConfState.Voters), nil
}

// PrepareControllerVoter moves mirrored state aside and starts voter-mode Raft plumbing.
func (r *Runtime) PrepareControllerVoter(ctx context.Context, req PrepareControllerVoterRequest) (PrepareControllerVoterResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return PrepareControllerVoterResult{}, err
	}
	if r == nil {
		return PrepareControllerVoterResult{}, ErrNotStarted
	}
	if req.NodeID != r.cfg.NodeID {
		return PrepareControllerVoterResult{}, fmt.Errorf("controllerv2: prepare controller voter node mismatch local=%d request=%d", r.cfg.NodeID, req.NodeID)
	}
	if req.ClusterID == "" || req.ClusterID != r.cfg.ClusterID {
		return PrepareControllerVoterResult{}, fmt.Errorf("controllerv2: prepare controller voter cluster mismatch local=%q request=%q", r.cfg.ClusterID, req.ClusterID)
	}
	if err := validatePrepareControllerVoterNextVoters(r.cfg.NodeID, req.NextVoters); err != nil {
		return PrepareControllerVoterResult{}, err
	}

	mirrorRefreshStopped := r.stopMirrorRefreshLoop()
	transitionStarted := false
	defer func() {
		if mirrorRefreshStopped && !transitionStarted {
			r.restartMirrorRefreshLoopIfStillMirror()
		}
	}()
	selection, err := r.selectPreservedMirrorState(ctx)
	if err != nil {
		return PrepareControllerVoterResult{}, err
	}
	st := selection.selected.state
	if st.ClusterID != req.ClusterID {
		return PrepareControllerVoterResult{}, fmt.Errorf("controllerv2: mirror state cluster mismatch local=%q request=%q", st.ClusterID, req.ClusterID)
	}
	if st.Revision < req.ExpectedRevision {
		return PrepareControllerVoterResult{}, ErrExpectedRevisionMismatch
	}
	if err := validatePrepareControllerVoterNextVotersForState(r.cfg.NodeID, r.cfg.Addr, st, req.NextVoters); err != nil {
		return PrepareControllerVoterResult{}, err
	}
	if r.controllerVoterPrepared() {
		return PrepareControllerVoterResult{Prepared: true, StateRevision: st.Revision}, nil
	}

	if r.raft != nil {
		r.clearControllerVoterRuntimeFields()
	}
	if err := moveMirrorStateAside(selection); err != nil {
		return PrepareControllerVoterResult{}, err
	}
	if err := r.publishState(st); err != nil {
		return PrepareControllerVoterResult{}, err
	}
	transitionStarted = true
	r.cfg.Role = RuntimeRoleVoter
	r.cfg.Voters = copyVoters(req.NextVoters)
	r.cfg.AllowBootstrap = false
	r.server = nil
	r.syncServer = nil
	r.syncClient = nil
	r.sm = nil
	r.store = statefile.New(filepath.Join(r.cfg.StateDir, "cluster-state.json"))
	if err := r.startVoter(ctx); err != nil {
		r.clearControllerVoterRuntimeFields()
		return PrepareControllerVoterResult{}, err
	}
	return PrepareControllerVoterResult{Prepared: true, StateRevision: st.Revision}, nil
}

func (r *Runtime) stopMirrorRefreshLoop() bool {
	if r.syncClient == nil || r.refreshCancel == nil {
		return false
	}
	r.stopRefreshLoop()
	return true
}

func (r *Runtime) restartMirrorRefreshLoopIfStillMirror() {
	if r.cfg.Role != RuntimeRoleMirror || r.syncClient == nil || r.refreshCancel != nil {
		return
	}
	r.startRefreshLoop()
}

func (r *Runtime) selectPreservedMirrorState(ctx context.Context) (mirrorStateSelection, error) {
	active, backup := r.loadMirrorStateCandidates(ctx)
	selected, err := selectPreservedMirrorState(active, backup)
	if err != nil {
		return mirrorStateSelection{}, err
	}
	return mirrorStateSelection{active: active, backup: backup, selected: selected}, nil
}

func moveMirrorStateAside(selection mirrorStateSelection) error {
	active := selection.active
	backup := selection.backup
	selected := selection.selected
	if selected.path == active.path {
		return replaceBackupWithActive(active.path, backup.path, backup.exists)
	}
	if active.exists {
		if err := os.Remove(active.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("controllerv2: remove active mirror state %s: %w", active.path, err)
		}
		if err := syncMirrorStateDir(active.path); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) loadMirrorStateCandidates(ctx context.Context) (mirrorStateCandidate, mirrorStateCandidate) {
	activePath := filepath.Join(r.cfg.StateDir, "cluster-state.json")
	backupPath := filepath.Join(r.cfg.StateDir, mirrorBeforeControllerVoterPromotionFile)
	return loadMirrorStateCandidate(ctx, activePath), loadMirrorStateCandidate(ctx, backupPath)
}

func loadMirrorStateCandidate(ctx context.Context, path string) mirrorStateCandidate {
	st, err := statefile.New(path).Load(ctx)
	if err == nil {
		return mirrorStateCandidate{path: path, state: st, exists: true, valid: true}
	}
	if errors.Is(err, os.ErrNotExist) {
		return mirrorStateCandidate{path: path}
	}
	return mirrorStateCandidate{path: path, exists: true, err: err}
}

func selectPreservedMirrorState(active mirrorStateCandidate, backup mirrorStateCandidate) (mirrorStateCandidate, error) {
	switch {
	case active.valid && backup.valid:
		if active.state.Revision > backup.state.Revision {
			return active, nil
		}
		if active.state.Revision == backup.state.Revision && active.state.Checksum != backup.state.Checksum {
			return active, nil
		}
		return backup, nil
	case active.valid:
		return active, nil
	case backup.valid:
		return backup, nil
	case active.exists && active.err != nil && backup.exists && backup.err != nil:
		return mirrorStateCandidate{}, fmt.Errorf("controllerv2: load active mirror state: %w; load backup mirror state: %v", active.err, backup.err)
	case active.exists && active.err != nil:
		return mirrorStateCandidate{}, active.err
	case backup.exists && backup.err != nil:
		return mirrorStateCandidate{}, backup.err
	default:
		return mirrorStateCandidate{}, fmt.Errorf("controllerv2: mirror state %s is not available", active.path)
	}
}

func replaceBackupWithActive(activePath string, backupPath string, backupExists bool) error {
	oldBackupPath := backupPath + ".old"
	if backupExists {
		if err := os.Remove(oldBackupPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("controllerv2: remove old mirror state backup %s: %w", oldBackupPath, err)
		}
		if err := os.Rename(backupPath, oldBackupPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("controllerv2: move stale mirror state backup %s to %s: %w", backupPath, oldBackupPath, err)
		}
	}
	if err := os.Rename(activePath, backupPath); err != nil {
		if backupExists {
			if restoreErr := os.Rename(oldBackupPath, backupPath); restoreErr == nil {
				_ = syncMirrorStateDir(backupPath)
			}
		}
		return fmt.Errorf("controllerv2: move mirror state %s to %s: %w", activePath, backupPath, err)
	}
	if backupExists {
		_ = os.Remove(oldBackupPath)
	}
	return syncMirrorStateDir(backupPath)
}

func syncMirrorStateDir(path string) error {
	dir := filepath.Dir(path)
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("controllerv2: open mirror state dir %s: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("controllerv2: fsync mirror state dir %s: %w", dir, err)
	}
	return nil
}

func validatePrepareControllerVoterNextVoters(localNodeID uint64, voters []Voter) error {
	if len(voters) == 0 {
		return fmt.Errorf("controllerv2: prepare controller voter requires next voters")
	}
	seen := make(map[uint64]struct{}, len(voters))
	localFound := false
	for _, voter := range voters {
		if voter.NodeID == 0 {
			return fmt.Errorf("controllerv2: prepare controller voter next voter node id must be > 0")
		}
		if voter.Addr == "" {
			return fmt.Errorf("controllerv2: prepare controller voter next voter %d requires addr", voter.NodeID)
		}
		if _, ok := seen[voter.NodeID]; ok {
			return fmt.Errorf("controllerv2: prepare controller voter duplicate next voter %d", voter.NodeID)
		}
		seen[voter.NodeID] = struct{}{}
		if voter.NodeID == localNodeID {
			localFound = true
		}
	}
	if !localFound {
		return fmt.Errorf("controllerv2: prepare controller voter next voters missing local node %d", localNodeID)
	}
	return nil
}

func validatePrepareControllerVoterNextVotersForState(localNodeID uint64, localAddr string, st ClusterState, voters []Voter) error {
	byNextVoter := make(map[uint64]Voter, len(voters))
	for _, voter := range voters {
		byNextVoter[voter.NodeID] = voter
	}
	allowed := make(map[uint64]struct{}, len(st.Controllers)+1)
	allowed[localNodeID] = struct{}{}
	for _, controller := range st.Controllers {
		allowed[controller.NodeID] = struct{}{}
		next, ok := byNextVoter[controller.NodeID]
		if !ok {
			return fmt.Errorf("controllerv2: prepare controller voter next voters missing current controller voter %d", controller.NodeID)
		}
		if next.Addr != controller.Addr {
			return fmt.Errorf("controllerv2: prepare controller voter next voter %d addr %q does not match controller addr %q", controller.NodeID, next.Addr, controller.Addr)
		}
	}
	local, ok := byNextVoter[localNodeID]
	if !ok {
		return fmt.Errorf("controllerv2: prepare controller voter next voters missing local node %d", localNodeID)
	}
	localNode, ok := findLifecycleNode(st, localNodeID)
	if !ok {
		return fmt.Errorf("controllerv2: prepare controller voter local node %d missing from preserved state", localNodeID)
	}
	if localNode.JoinState != NodeJoinStateActive {
		return fmt.Errorf("controllerv2: prepare controller voter local node %d is not active", localNodeID)
	}
	if local.Addr != localNode.Addr {
		return fmt.Errorf("controllerv2: prepare controller voter local next voter %d addr %q does not match preserved addr %q", localNodeID, local.Addr, localNode.Addr)
	}
	if localAddr != "" && localAddr != localNode.Addr {
		return fmt.Errorf("controllerv2: prepare controller voter local addr %q does not match preserved addr %q", localAddr, localNode.Addr)
	}
	for _, voter := range voters {
		if _, ok := allowed[voter.NodeID]; !ok {
			return fmt.Errorf("controllerv2: prepare controller voter next voter %d is not a current controller or local node", voter.NodeID)
		}
	}
	return nil
}

func (r *Runtime) controllerVoterPrepared() bool {
	return r.cfg.Role == RuntimeRoleVoter &&
		r.syncClient == nil &&
		r.sm != nil &&
		r.raft != nil &&
		r.server != nil &&
		r.syncServer != nil
}

func (r *Runtime) clearControllerVoterRuntimeFields() {
	if r.raft != nil {
		_ = r.raft.Stop()
	}
	r.sm = nil
	r.raft = nil
	r.server = nil
	r.syncServer = nil
}

func copyOptionalUint64s(in []uint64) []uint64 {
	if in == nil {
		return nil
	}
	out := make([]uint64, len(in))
	copy(out, in)
	return out
}

func copyVoters(in []Voter) []Voter {
	out := make([]Voter, len(in))
	copy(out, in)
	return out
}

func controllerNodeIDsForRuntime(in []ControllerVoter) []uint64 {
	out := make([]uint64, 0, len(in))
	for _, voter := range in {
		out = append(out, voter.NodeID)
	}
	return out
}

func containsRuntimeUint64(values []uint64, want uint64) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sameRuntimeUint64Set(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[uint64]struct{}, len(left))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		if _, ok := seen[value]; !ok {
			return false
		}
	}
	return true
}

func cloneSortedUint64s(in []uint64) []uint64 {
	out := append([]uint64(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func controllerRaftStatusProofIndex(status RaftStatus) uint64 {
	switch {
	case status.AppliedIndex != 0:
		return status.AppliedIndex
	case status.CommitIndex != 0:
		return status.CommitIndex
	default:
		return status.LastIndex
	}
}
