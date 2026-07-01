package management

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
)

func TestPromoteControllerVoterHappyPathCarriesLiveProof(t *testing.T) {
	snapshot := controllerVoterPromotionSnapshot()
	promoter := &fakeControllerVoterPromoter{
		result: control.PromoteControllerVoterResult{
			Changed:        true,
			Node:           control.Node{NodeID: 4, Addr: "10.0.0.4:11110", Roles: []control.Role{control.RoleController, control.RoleData}},
			Revision:       10,
			PreviousVoters: []uint64{1, 2},
			NextVoters:     []uint64{1, 2, 4},
			Warnings:       []string{"controller_voter_count_even"},
		},
	}
	preparer := &fakeControllerVoterPreparer{
		response: PrepareControllerVoterResponse{
			NodeID:              4,
			Prepared:            true,
			StateRevision:       9,
			ObservedConfigIndex: 77,
			ObservedVoters:      []uint64{4, 2, 1},
		},
	}
	observer := &recordingControllerVoterPromotionObserver{}
	app := New(Options{
		Cluster:                          fakeNodeSnapshotReader{snapshot: snapshot},
		ControllerVoterPromoter:          promoter,
		ControllerVoterReadiness:         fakeControllerVoterReadiness{readiness: readyControllerVoterReadiness(4, snapshot.ClusterID, snapshot.Revision)},
		ControllerVoterPreparer:          preparer,
		ControllerVoterPromotionObserver: observer,
	})

	resp, err := app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 4, ExpectedRevision: 9})
	if err != nil {
		t.Fatalf("PromoteControllerVoter() error = %v", err)
	}
	if !resp.Changed || resp.NodeID != 4 || resp.StateRevision != 10 {
		t.Fatalf("PromoteControllerVoter() = %#v, want changed node 4 revision 10", resp)
	}
	if !sameUint64Slice(resp.PreviousVoters, []uint64{1, 2}) || !sameUint64Slice(resp.NextVoters, []uint64{1, 2, 4}) {
		t.Fatalf("response voters = prev %#v next %#v, want [1 2] -> [1 2 4]", resp.PreviousVoters, resp.NextVoters)
	}
	if promoter.calls != 1 {
		t.Fatalf("promoter calls = %d, want 1", promoter.calls)
	}
	if promoter.request.NodeID != 4 || promoter.request.ExpectedRevision != 9 ||
		!sameUint64Slice(promoter.request.ExpectedVoters, []uint64{1, 2}) ||
		promoter.request.ObservedConfigIndex != 77 ||
		!sameUint64Slice(promoter.request.ObservedVoters, []uint64{4, 2, 1}) {
		t.Fatalf("writer request = %#v, want expected voters and live proof preserved", promoter.request)
	}
	if promoter.request.ExpectedVoters == nil {
		t.Fatal("writer ExpectedVoters is nil, want explicit previous voter set")
	}
	if preparer.calls != 1 {
		t.Fatalf("preparer calls = %d, want 1", preparer.calls)
	}
	if preparer.request.NodeID != 4 || preparer.request.ClusterID != snapshot.ClusterID || preparer.request.ExpectedRevision != 9 {
		t.Fatalf("prepare request identity = %#v, want node 4 cluster-a revision 9", preparer.request)
	}
	wantEndpoints := []ControllerVoterEndpoint{{NodeID: 1, Addr: "10.0.0.1:11110"}, {NodeID: 2, Addr: "10.0.0.2:11110"}, {NodeID: 4, Addr: "10.0.0.4:11110"}}
	if len(preparer.request.NextVoters) != len(wantEndpoints) {
		t.Fatalf("prepare endpoints len = %d, want %d: %#v", len(preparer.request.NextVoters), len(wantEndpoints), preparer.request.NextVoters)
	}
	for i := range wantEndpoints {
		if preparer.request.NextVoters[i] != wantEndpoints[i] {
			t.Fatalf("prepare endpoint[%d] = %#v, want %#v", i, preparer.request.NextVoters[i], wantEndpoints[i])
		}
	}
	if !sameControllerVoterPromotionStrings(observer.attempts, []string{"changed"}) {
		t.Fatalf("attempts = %v, want [changed]", observer.attempts)
	}
	if !sameControllerVoterPromotionStrings(observer.phases, []string{"readiness", "prepare", "commit_state"}) {
		t.Fatalf("phases = %v, want readiness/prepare/commit_state", observer.phases)
	}
}

func TestPromoteControllerVoterBlocksStaleHealthBeforePrepare(t *testing.T) {
	snapshot := controllerVoterPromotionSnapshot()
	snapshot.Nodes[2].Health.Freshness = control.NodeHealthStale
	promoter := &fakeControllerVoterPromoter{}
	preparer := &fakeControllerVoterPreparer{}
	observer := &recordingControllerVoterPromotionObserver{}
	app := New(Options{
		Cluster:                          fakeNodeSnapshotReader{snapshot: snapshot},
		ControllerVoterPromoter:          promoter,
		ControllerVoterReadiness:         fakeControllerVoterReadiness{readiness: readyControllerVoterReadiness(4, snapshot.ClusterID, snapshot.Revision)},
		ControllerVoterPreparer:          preparer,
		ControllerVoterPromotionObserver: observer,
	})

	_, err := app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 4})
	assertControllerVoterBlocked(t, err, "target_health_stale")
	if preparer.calls != 0 || promoter.calls != 0 {
		t.Fatalf("preparer/promoter calls = %d/%d, want 0/0", preparer.calls, promoter.calls)
	}
	if !sameControllerVoterPromotionStrings(observer.attempts, []string{"blocked"}) || !sameControllerVoterPromotionStrings(observer.blockers, []string{"target_health_stale"}) {
		t.Fatalf("observer attempts=%v blockers=%v, want blocked target_health_stale", observer.attempts, observer.blockers)
	}
}

func TestPromoteControllerVoterBlocksEmptyReadinessClusterIDBeforePrepare(t *testing.T) {
	snapshot := controllerVoterPromotionSnapshot()
	promoter := &fakeControllerVoterPromoter{}
	preparer := &fakeControllerVoterPreparer{}
	readiness := readyControllerVoterReadiness(4, "", snapshot.Revision)
	app := New(Options{
		Cluster:                  fakeNodeSnapshotReader{snapshot: snapshot},
		ControllerVoterPromoter:  promoter,
		ControllerVoterReadiness: fakeControllerVoterReadiness{readiness: readiness},
		ControllerVoterPreparer:  preparer,
	})

	_, err := app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 4})
	assertControllerVoterBlocked(t, err, "target_cluster_mismatch")
	if preparer.calls != 0 || promoter.calls != 0 {
		t.Fatalf("preparer/promoter calls = %d/%d, want 0/0", preparer.calls, promoter.calls)
	}
}

func TestPromoteControllerVoterAlreadyVoterNoopSkipsLivePorts(t *testing.T) {
	snapshot := controllerVoterPromotionSnapshot()
	promoter := &fakeControllerVoterPromoter{}
	preparer := &fakeControllerVoterPreparer{}
	app := New(Options{
		Cluster:                  fakeNodeSnapshotReader{snapshot: snapshot},
		ControllerVoterPromoter:  promoter,
		ControllerVoterPreparer:  preparer,
		ControllerVoterReadiness: fakeControllerVoterReadiness{readiness: readyControllerVoterReadiness(1, snapshot.ClusterID, snapshot.Revision)},
	})

	resp, err := app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 1})
	if err != nil {
		t.Fatalf("PromoteControllerVoter() error = %v", err)
	}
	if resp.Changed || resp.NodeID != 1 || resp.StateRevision != 9 ||
		!sameUint64Slice(resp.PreviousVoters, []uint64{1, 2}) ||
		!sameUint64Slice(resp.NextVoters, []uint64{1, 2}) {
		t.Fatalf("noop response = %#v, want unchanged current durable voters", resp)
	}
	if preparer.calls != 0 || promoter.calls != 0 {
		t.Fatalf("preparer/promoter calls = %d/%d, want 0/0", preparer.calls, promoter.calls)
	}
}

func TestPromoteControllerVoterBlocksExpectedRevisionMismatch(t *testing.T) {
	app := New(Options{
		Cluster: fakeNodeSnapshotReader{snapshot: controllerVoterPromotionSnapshot()},
	})

	_, err := app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 4, ExpectedRevision: 8})
	assertControllerVoterBlocked(t, err, "expected_revision_mismatch")
}

func TestPromoteControllerVoterBlocksInvalidPrepareProof(t *testing.T) {
	tests := []struct {
		name     string
		response PrepareControllerVoterResponse
		reason   string
	}{
		{
			name:     "missing config index",
			response: PrepareControllerVoterResponse{NodeID: 4, Prepared: true, StateRevision: 9, ObservedVoters: []uint64{1, 2, 4}},
			reason:   "target_controller_raft_proof_missing",
		},
		{
			name:     "voter mismatch",
			response: PrepareControllerVoterResponse{NodeID: 4, Prepared: true, StateRevision: 9, ObservedConfigIndex: 77, ObservedVoters: []uint64{1, 2}},
			reason:   "target_controller_raft_voters_mismatch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := controllerVoterPromotionSnapshot()
			promoter := &fakeControllerVoterPromoter{}
			app := New(Options{
				Cluster:                  fakeNodeSnapshotReader{snapshot: snapshot},
				ControllerVoterPromoter:  promoter,
				ControllerVoterReadiness: fakeControllerVoterReadiness{readiness: readyControllerVoterReadiness(4, snapshot.ClusterID, snapshot.Revision)},
				ControllerVoterPreparer:  &fakeControllerVoterPreparer{response: tt.response},
			})

			_, err := app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 4})
			assertControllerVoterBlocked(t, err, tt.reason)
			if promoter.calls != 0 {
				t.Fatalf("promoter calls = %d, want 0", promoter.calls)
			}
		})
	}
}

func TestPromoteControllerVoterReadinessErrorsPreserveContextCause(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := controllerVoterPromotionSnapshot()
			preparer := &fakeControllerVoterPreparer{}
			promoter := &fakeControllerVoterPromoter{}
			app := New(Options{
				Cluster:                  fakeNodeSnapshotReader{snapshot: snapshot},
				ControllerVoterPromoter:  promoter,
				ControllerVoterReadiness: fakeControllerVoterReadiness{err: tt.err},
				ControllerVoterPreparer:  preparer,
			})

			_, err := app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 4})
			if !errors.Is(err, tt.err) {
				t.Fatalf("PromoteControllerVoter() error = %v, want context cause %v", err, tt.err)
			}
			if preparer.calls != 0 || promoter.calls != 0 {
				t.Fatalf("preparer/promoter calls = %d/%d, want 0/0", preparer.calls, promoter.calls)
			}
		})
	}
}

func TestPromoteControllerVoterPrepareErrorMapping(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		wantErr         error
		wantExpectedRev bool
	}{
		{
			name:            "expected revision mismatch",
			err:             cv2.ErrExpectedRevisionMismatch,
			wantErr:         ErrControllerVoterPromotionBlocked,
			wantExpectedRev: true,
		},
		{
			name:    "remote blocked remains blocked",
			err:     ErrControllerVoterPromotionBlocked,
			wantErr: ErrControllerVoterPromotionBlocked,
		},
		{name: "canceled", err: context.Canceled, wantErr: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, wantErr: context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := controllerVoterPromotionSnapshot()
			promoter := &fakeControllerVoterPromoter{}
			app := New(Options{
				Cluster:                  fakeNodeSnapshotReader{snapshot: snapshot},
				ControllerVoterPromoter:  promoter,
				ControllerVoterReadiness: fakeControllerVoterReadiness{readiness: readyControllerVoterReadiness(4, snapshot.ClusterID, snapshot.Revision)},
				ControllerVoterPreparer:  &fakeControllerVoterPreparer{err: tt.err},
			})

			_, err := app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 4})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("PromoteControllerVoter() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == ErrControllerVoterPromotionBlocked && errors.Is(err, ErrControllerVoterPromotionUnavailable) {
				t.Fatalf("PromoteControllerVoter() error = %v, want blocked without unavailable", err)
			}
			if tt.wantExpectedRev && !cv2.IsExpectedRevisionMismatch(err) {
				t.Fatalf("PromoteControllerVoter() error = %v, want expected revision mismatch preserved", err)
			}
			if promoter.calls != 0 {
				t.Fatalf("promoter calls = %d, want 0", promoter.calls)
			}
		})
	}
}

func TestPromoteControllerVoterUnavailablePorts(t *testing.T) {
	snapshot := controllerVoterPromotionSnapshot()
	tests := []struct {
		name string
		app  *App
	}{
		{name: "nil app", app: nil},
		{name: "missing cluster", app: New(Options{})},
		{name: "missing promoter", app: New(Options{
			Cluster:                  fakeNodeSnapshotReader{snapshot: snapshot},
			ControllerVoterReadiness: fakeControllerVoterReadiness{readiness: readyControllerVoterReadiness(4, snapshot.ClusterID, snapshot.Revision)},
			ControllerVoterPreparer:  &fakeControllerVoterPreparer{},
		})},
		{name: "missing readiness", app: New(Options{
			Cluster:                 fakeNodeSnapshotReader{snapshot: snapshot},
			ControllerVoterPromoter: &fakeControllerVoterPromoter{},
			ControllerVoterPreparer: &fakeControllerVoterPreparer{},
		})},
		{name: "missing preparer", app: New(Options{
			Cluster:                  fakeNodeSnapshotReader{snapshot: snapshot},
			ControllerVoterPromoter:  &fakeControllerVoterPromoter{},
			ControllerVoterReadiness: fakeControllerVoterReadiness{readiness: readyControllerVoterReadiness(4, snapshot.ClusterID, snapshot.Revision)},
		})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.app.PromoteControllerVoter(context.Background(), PromoteControllerVoterRequest{NodeID: 4})
			if !errors.Is(err, ErrControllerVoterPromotionUnavailable) {
				t.Fatalf("PromoteControllerVoter() error = %v, want %v", err, ErrControllerVoterPromotionUnavailable)
			}
		})
	}
}

func controllerVoterPromotionSnapshot() control.Snapshot {
	return control.Snapshot{
		ClusterID:    "cluster-a",
		Revision:     9,
		ControllerID: 1,
		Nodes: []control.Node{
			controllerVoterPromotionNode(1, "10.0.0.1:11110", []control.Role{control.RoleController, control.RoleData}, 9),
			controllerVoterPromotionNode(2, "10.0.0.2:11110", []control.Role{control.RoleController, control.RoleData}, 9),
			controllerVoterPromotionNode(4, "10.0.0.4:11110", []control.Role{control.RoleData}, 9),
		},
	}
}

func controllerVoterPromotionNode(nodeID uint64, addr string, roles []control.Role, revision uint64) control.Node {
	return control.Node{
		NodeID:    nodeID,
		Addr:      addr,
		Roles:     roles,
		Status:    control.NodeAlive,
		JoinState: control.NodeJoinStateActive,
		Health: control.NodeHealth{
			Status:                  control.NodeAlive,
			Freshness:               control.NodeHealthFresh,
			RuntimeReady:            true,
			ObservedControlRevision: revision,
		},
	}
}

func readyControllerVoterReadiness(nodeID uint64, clusterID string, revision uint64) ControllerVoterReadiness {
	return ControllerVoterReadiness{
		NodeID:         nodeID,
		ClusterID:      clusterID,
		Reachable:      true,
		TransportReady: true,
		ControlReady:   true,
		RuntimeReady:   true,
		CanPrepare:     true,
		MirrorRevision: revision,
	}
}

func assertControllerVoterBlocked(t *testing.T, err error, reason string) {
	t.Helper()
	if !errors.Is(err, ErrControllerVoterPromotionBlocked) {
		t.Fatalf("error = %v, want %v", err, ErrControllerVoterPromotionBlocked)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Fatalf("error = %v, want reason %q", err, reason)
	}
}

func sameControllerVoterPromotionStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

type recordingControllerVoterPromotionObserver struct {
	attempts []string
	blockers []string
	phases   []string
}

func (o *recordingControllerVoterPromotionObserver) ObserveControllerVoterPromotionAttempt(result string) {
	o.attempts = append(o.attempts, result)
}

func (o *recordingControllerVoterPromotionObserver) ObserveControllerVoterPromotionBlocker(reason string) {
	o.blockers = append(o.blockers, reason)
}

func (o *recordingControllerVoterPromotionObserver) ObserveControllerVoterPromotionPhase(phase string, _ time.Duration) {
	o.phases = append(o.phases, phase)
}

type fakeControllerVoterPromoter struct {
	request control.PromoteControllerVoterRequest
	result  control.PromoteControllerVoterResult
	calls   int
	err     error
}

func (f *fakeControllerVoterPromoter) PromoteControllerVoter(_ context.Context, req control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error) {
	f.calls++
	f.request = req
	return f.result, f.err
}

type fakeControllerVoterReadiness struct {
	readiness ControllerVoterReadiness
	err       error
}

func (f fakeControllerVoterReadiness) ControllerVoterReadiness(context.Context, uint64) (ControllerVoterReadiness, error) {
	return f.readiness, f.err
}

type fakeControllerVoterPreparer struct {
	request  PrepareControllerVoterRequest
	response PrepareControllerVoterResponse
	calls    int
	err      error
}

func (f *fakeControllerVoterPreparer) PrepareControllerVoter(_ context.Context, req PrepareControllerVoterRequest) (PrepareControllerVoterResponse, error) {
	f.calls++
	f.request = req
	return f.response, f.err
}
