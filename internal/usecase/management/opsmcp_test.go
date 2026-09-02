package management

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

type opsMCPAuditStub struct {
	entries []opscontract.AuditEntry
	err     error
	limit   *int
}

func (s opsMCPAuditStub) RecentAudits(_ context.Context, limit int) ([]opscontract.AuditEntry, error) {
	if s.limit != nil {
		*s.limit = limit
	}
	if s.err != nil {
		return nil, s.err
	}
	return append([]opscontract.AuditEntry(nil), s.entries...), nil
}

type fakeOpsMCPStore struct {
	snapshot control.Snapshot
	writes   int
}

func (s *fakeOpsMCPStore) NodeID() uint64 { return 1 }

func (s *fakeOpsMCPStore) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return s.snapshot.Clone(), nil
}

func (s *fakeOpsMCPStore) ReplaceOpsMCPState(_ context.Context, expectedRevision uint64, replacement controller.OpsMCPState) error {
	if expectedRevision != s.snapshot.Revision {
		return controller.ErrExpectedRevisionMismatch
	}
	s.writes++
	s.snapshot.Revision++
	state := control.OpsMCPState{
		Enabled:                     replacement.Enabled,
		OwnerNodeID:                 replacement.OwnerNodeID,
		ProfileFenceUntilUnixMillis: replacement.ProfileFenceUntilUnixMillis,
		Credentials:                 make([]control.OpsMCPCredential, 0, len(replacement.Credentials)),
	}
	for _, credential := range replacement.Credentials {
		state.Credentials = append(state.Credentials, control.OpsMCPCredential{
			ID: credential.ID, DigestSHA256: credential.DigestSHA256, CreatedAtUnixMillis: credential.CreatedAtUnixMillis,
		})
	}
	s.snapshot.OpsMCP = &state
	return nil
}

func TestOpsMCPCreateTokenIsOneTimeAndIdempotent(t *testing.T) {
	now := time.UnixMilli(1710000001000).UTC()
	store := newFakeOpsMCPStore()
	app := New(Options{Cluster: store, OpsMCP: store, Now: func() time.Time { return now }})
	request := OpsMCPTokenCreateRequest{ExpectedRevision: 7, IdempotencyKey: "create-token-1"}

	first, err := app.CreateOpsMCPToken(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateOpsMCPToken() error = %v", err)
	}
	if !strings.HasPrefix(first.Token, "wko_"+first.CredentialID+"_") || first.Token == "" {
		t.Fatalf("token = %q, credential = %q", first.Token, first.CredentialID)
	}
	if first.CreatedAtUnixMillis != now.UnixMilli() || first.Revision != 8 {
		t.Fatalf("response = %#v, want timestamp %d revision 8", first, now.UnixMilli())
	}

	retry, err := app.CreateOpsMCPToken(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateOpsMCPToken(retry) error = %v", err)
	}
	if retry != first || store.writes != 1 {
		t.Fatalf("retry = %#v writes=%d, want exact one-time response and one write", retry, store.writes)
	}
	status, err := app.OpsMCPStatus(context.Background())
	if err != nil {
		t.Fatalf("OpsMCPStatus() error = %v", err)
	}
	if len(status.Credentials) != 1 || status.Credentials[0].ID != first.CredentialID {
		t.Fatalf("status credentials = %#v", status.Credentials)
	}
}

func TestOpsMCPRequiresStopBeforeOwnerChangeAndLastTokenRevocation(t *testing.T) {
	store := newFakeOpsMCPStore()
	store.snapshot.OpsMCP = &control.OpsMCPState{
		Enabled:     true,
		OwnerNodeID: 1,
		Credentials: []control.OpsMCPCredential{{
			ID: "token-a", DigestSHA256: strings.Repeat("a", 64), CreatedAtUnixMillis: 1710000001000,
		}},
	}
	app := New(Options{Cluster: store, OpsMCP: store})

	err := app.SetOpsMCPOwner(context.Background(), OpsMCPOwnerUpdateRequest{
		ExpectedRevision: 7, IdempotencyKey: "owner-1", OwnerNodeID: 2,
	})
	if !errors.Is(err, ErrOpsMCPConflict) {
		t.Fatalf("SetOpsMCPOwner() error = %v, want conflict", err)
	}
	err = app.RevokeOpsMCPToken(context.Background(), OpsMCPTokenRevokeRequest{
		ExpectedRevision: 7, IdempotencyKey: "revoke-1", CredentialID: "token-a",
	})
	if !errors.Is(err, ErrOpsMCPConflict) {
		t.Fatalf("RevokeOpsMCPToken() error = %v, want conflict", err)
	}
	if store.writes != 0 {
		t.Fatalf("writes = %d, want no unsafe state write", store.writes)
	}
}

func TestOpsMCPAuditsProjectsBoundedRuntimeEntries(t *testing.T) {
	startedAt := time.Unix(42, 0).UTC()
	app := New(Options{OpsMCPAudit: opsMCPAuditStub{entries: []opscontract.AuditEntry{{
		RequestID: "request-1", CredentialID: "credential-1", Tool: "cluster_health",
		Result: "ok", StartedAt: startedAt, DurationMS: 7, ResponseBytes: 123, CacheHit: true,
		Target:    opscontract.AuditTarget{NodeID: 2, SlotID: 3, ChannelType: 4},
		PprofKind: "cpu", PprofSeconds: 10,
	}}}})

	entries, err := app.OpsMCPAudits(context.Background(), 10)
	if err != nil {
		t.Fatalf("OpsMCPAudits() error = %v", err)
	}
	if len(entries) != 1 || entries[0].RequestID != "request-1" || entries[0].StartedAt != startedAt ||
		entries[0].ResponseBytes != 123 || !entries[0].CacheHit ||
		entries[0].NodeID != 2 || entries[0].SlotID != 3 || entries[0].ChannelType != 4 ||
		entries[0].PprofKind != "cpu" || entries[0].PprofSeconds != 10 {
		t.Fatalf("OpsMCPAudits() = %#v", entries)
	}
}

func TestOpsMCPAuditsRejectsUnboundedReadsAndPreservesUnavailableState(t *testing.T) {
	for _, limit := range []int{0, 201} {
		if _, err := (&App{}).OpsMCPAudits(context.Background(), limit); !errors.Is(err, ErrOpsMCPInvalidRequest) {
			t.Fatalf("limit %d error = %v", limit, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&App{}).OpsMCPAudits(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled audit error = %v", err)
	}

	entries, err := (&App{}).OpsMCPAudits(context.Background(), 1)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unwired audits = %#v, %v", entries, err)
	}

	seenLimit := 0
	app := New(Options{OpsMCPAudit: opsMCPAuditStub{err: errors.New("audit store unavailable"), limit: &seenLimit}})
	if _, err := app.OpsMCPAudits(context.Background(), 200); !errors.Is(err, ErrOpsMCPUnavailable) {
		t.Fatalf("audit provider error = %v", err)
	}
	if seenLimit != 200 {
		t.Fatalf("provider limit = %d, want 200", seenLimit)
	}
}

func TestOpsMCPStopPersistsProfileFenceAndDownOwnerIsUnavailable(t *testing.T) {
	now := time.Date(2026, 7, 24, 5, 0, 0, 0, time.UTC)
	store := newFakeOpsMCPStore()
	store.snapshot.OpsMCP = &control.OpsMCPState{
		Enabled: true, OwnerNodeID: 1,
		Credentials: []control.OpsMCPCredential{{
			ID: "token-a", DigestSHA256: strings.Repeat("a", 64), CreatedAtUnixMillis: now.UnixMilli(),
		}},
	}
	app := New(Options{Cluster: store, OpsMCP: store, Now: func() time.Time { return now }})
	if err := app.StopOpsMCP(context.Background(), OpsMCPStateMutationRequest{
		ExpectedRevision: 7, IdempotencyKey: "stop-1",
	}); err != nil {
		t.Fatalf("StopOpsMCP() error = %v", err)
	}
	if got := store.snapshot.OpsMCP.ProfileFenceUntilUnixMillis; got != now.Add(opsMCPProfileFence).UnixMilli() {
		t.Fatalf("profile fence = %d, want %d", got, now.Add(opsMCPProfileFence).UnixMilli())
	}

	store.snapshot.OpsMCP.Enabled = true
	store.snapshot.Nodes[0].Status = control.NodeDown
	status, err := app.OpsMCPStatus(context.Background())
	if err != nil {
		t.Fatalf("OpsMCPStatus() error = %v", err)
	}
	if status.ObservedStatus != "unavailable" {
		t.Fatalf("observed status = %q, want unavailable", status.ObservedStatus)
	}
	for _, candidate := range status.OwnerCandidates {
		if candidate.NodeID == 1 {
			t.Fatalf("down owner appeared in candidates: %#v", status.OwnerCandidates)
		}
	}
}

func TestOpsMCPStartRequiresActiveOwnerAndCredentialAndIsIdempotent(t *testing.T) {
	store := newFakeOpsMCPStore()
	store.snapshot.OpsMCP = &control.OpsMCPState{
		OwnerNodeID: 2,
		Credentials: []control.OpsMCPCredential{{
			ID: "token-a", DigestSHA256: strings.Repeat("a", 64), CreatedAtUnixMillis: 1710000001000,
		}},
	}
	app := New(Options{Cluster: store, OpsMCP: store})
	req := OpsMCPStateMutationRequest{ExpectedRevision: 7, IdempotencyKey: "start-1"}

	if err := app.StartOpsMCP(context.Background(), req); err != nil {
		t.Fatalf("StartOpsMCP() error = %v", err)
	}
	if store.snapshot.OpsMCP == nil || !store.snapshot.OpsMCP.Enabled || store.snapshot.OpsMCP.OwnerNodeID != 2 || store.writes != 1 {
		t.Fatalf("started state = %#v writes=%d", store.snapshot.OpsMCP, store.writes)
	}
	if err := app.StartOpsMCP(context.Background(), req); err != nil {
		t.Fatalf("StartOpsMCP(retry) error = %v", err)
	}
	if store.writes != 1 {
		t.Fatalf("idempotent retry writes = %d, want 1", store.writes)
	}

	missingPrerequisite := newFakeOpsMCPStore()
	missingPrerequisite.snapshot.OpsMCP = &control.OpsMCPState{}
	unsafeApp := New(Options{Cluster: missingPrerequisite, OpsMCP: missingPrerequisite})
	if err := unsafeApp.StartOpsMCP(context.Background(), OpsMCPStateMutationRequest{
		ExpectedRevision: 7, IdempotencyKey: "start-without-owner",
	}); !errors.Is(err, ErrOpsMCPConflict) {
		t.Fatalf("start without owner/token error = %v", err)
	}
	if missingPrerequisite.writes != 0 {
		t.Fatalf("unsafe start writes = %d", missingPrerequisite.writes)
	}
}

func newFakeOpsMCPStore() *fakeOpsMCPStore {
	return &fakeOpsMCPStore{snapshot: control.Snapshot{
		ClusterID: "cluster-a",
		Revision:  7,
		Nodes: []control.Node{
			{NodeID: 1, JoinState: control.NodeJoinStateActive, Status: control.NodeAlive, Roles: []control.Role{control.RoleData}},
			{NodeID: 2, JoinState: control.NodeJoinStateActive, Status: control.NodeAlive, Roles: []control.Role{control.RoleData}},
		},
	}}
}
