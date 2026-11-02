//go:build integration

package presence_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/presencelog"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	p "github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// Approved failure inventory: Claim, concurrent Begin, page, End, generation
// validation/publication, deadline/cancellation and unrelated follower errors
// must retain their own immutable step evidence without publishing offline proof.
// Successful recovery and joined Close must preserve their existing semantics.
// Each slice below drives the public recovery interface with external ports.
type fullFailurePorts struct {
	*slotBeginFixture
	claimFailure error
	failStep     string
	ended        atomic.Int32
	failTarget   uint16
}

func (f *fullFailurePorts) ClaimSlotGeneration(ctx context.Context, target p.RouteTarget) (p.GenerationClaim, []uint64, error) {
	if f.failStep == "chain_deadline" {
		<-ctx.Done()
		return p.GenerationClaim{}, nil, ctx.Err()
	}
	if f.claimFailure != nil && (f.failTarget == 0 || target.HashSlot == f.failTarget) {
		return p.GenerationClaim{}, nil, f.claimFailure
	}
	return f.slotBeginFixture.ClaimSlotGeneration(ctx, target)
}

func (f *fullFailurePorts) BeginSlot(ctx context.Context, claim p.GenerationClaim, id uint64) (p.OwnerCut, error) {
	if f.failStep == "owner_begin" && id == 2 {
		return p.OwnerCut{}, authority.ErrRouteNotReady
	}
	return f.slotBeginFixture.BeginSlot(ctx, claim, id)
}

func (f *fullFailurePorts) ReadSlotPage(ctx context.Context, req p.SlotPageRequest, budget *p.RecoveryResultBudget) (*p.OwnedSlotPage, error) {
	if f.failStep == "owner_page" {
		return nil, authority.ErrRouteNotReady
	}
	if f.failStep == "malformed_page" {
		return &p.OwnedSlotPage{}, nil
	}
	page, err := f.slotBeginFixture.ReadSlotPage(ctx, req, budget)
	if err == nil && f.failStep == "directory_install" {
		err = f.directory.AbortGeneration(req.Claim)
	}
	return page, err
}

func (f *fullFailurePorts) EndSlot(ctx context.Context, req p.SlotEndRequest) (p.SlotEnd, error) {
	if f.failStep == "owner_end" {
		return p.SlotEnd{}, authority.ErrRouteNotReady
	}
	end, err := f.slotBeginFixture.EndSlot(ctx, req)
	if err == nil {
		f.ended.Add(1)
		if f.failStep == "directory_end" {
			err = f.directory.AbortGeneration(req.Claim)
		}
	}
	return end, err
}

func (f *fullFailurePorts) ValidateSlotGeneration(ctx context.Context, claim p.GenerationClaim) error {
	if f.failStep == "directory_begin" {
		next := claim.Target
		next.LeaderTerm++
		f.directory.BecomeAuthority(next)
	}
	if f.failStep == "directory_complete" && f.ended.Load() == 3 {
		return f.directory.AbortGeneration(claim)
	}
	if f.failStep == "validate_before" {
		return authority.ErrRouteNotReady
	}
	if f.failStep == "validate_after" {
		if _, complete, _ := f.directory.CurrentGeneration(claim.Target); !complete {
			// The real public Directory has reached its all-owner cut, but has
			// not yet published Complete when the second source check runs.
			if f.aborted.Load() == 0 && f.ended.Load() == 3 {
				return authority.ErrStaleGeneration
			}
		}
	}
	return f.slotBeginFixture.ValidateSlotGeneration(ctx, claim)
}

// The data-only error capability is observed through errors.As, so the first
// regression compiles against the old implementation and fails on missing data.
type fullFailureDiagnostic interface {
	FullRecoveryFailure() (stage, kind string, elapsed, effectiveBudget time.Duration)
}

func newFullFailureRecovery(t *testing.T) (*fullFailurePorts, *p.SlotRecovery, *p.AuthorityRecovery, p.RouteTarget) {
	t.Helper()
	directory := authority.NewDirectory(authority.DirectoryOptions{LocalNodeID: 1, RequireRecovery: true})
	target := p.RouteTarget{HashSlot: 15, SlotID: 3, LeaderNodeID: 1, LeaderTerm: 7, ConfigEpoch: 9}
	directory.BecomeAuthority(target)
	base := &slotBeginFixture{slotFairFixture: &slotFairFixture{claims: make(map[uint16]p.GenerationClaim), directory: directory}, owners: make(map[uint64]*p.OwnerGeneration)}
	for id := uint64(1); id <= 3; id++ {
		base.ids = append(base.ids, id)
		base.owners[id] = p.NewOwnerGeneration(online.NewRegistry(online.RegistryOptions{}), id, 10+id, base.ValidateSlotGeneration)
	}
	ports := &fullFailurePorts{slotBeginFixture: base}
	slots := p.NewSlotRecovery(directory, ports, ports)
	recovery := p.NewAuthorityRecovery(slots, slots)
	t.Cleanup(func() { closeFullFailureRecovery(t, recovery, slots) })
	return ports, slots, recovery, target
}

func closeFullFailureRecovery(t *testing.T, recovery *p.AuthorityRecovery, slots *p.SlotRecovery) {
	t.Helper()
	cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := recovery.Close(cleanup); err != nil {
		t.Error(err)
	}
	if err := slots.Close(cleanup); err != nil {
		t.Error(err)
	}
}

func TestFullRecoveryClaimFailureRetainsStepEvidence(t *testing.T) {
	ports, slots, recovery, target := newFullFailureRecovery(t)
	ports.claimFailure = authority.ErrRouteNotReady
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		if err := recovery.Close(cleanup); err != nil {
			t.Error(err)
		}
		if err := slots.Close(cleanup); err != nil {
			t.Error(err)
		}
	}()
	_, err := recovery.Endpoints(ctx, target, []string{"offline"})
	if !errors.Is(err, authority.ErrRouteNotReady) {
		t.Errorf("original error family changed: %v", err)
	}
	var diagnostic fullFailureDiagnostic
	observed := errors.As(err, &diagnostic)
	stage, kind := "", ""
	var elapsed, budget time.Duration
	if observed {
		stage, kind, elapsed, budget = diagnostic.FullRecoveryFailure()
	}
	if !observed || stage != "claim" || kind != "route_not_ready" || elapsed < 0 || budget <= 0 || budget > 2*time.Second {
		t.Errorf("missing exact Full Claim failure evidence: observed=%t stage=%q kind=%q elapsed=%s effective_budget=%s", observed, stage, kind, elapsed, budget)
	}
	frozen, known := p.FullRecoveryFailure(err)
	frozen.Stage = "caller-mutated-private-value"
	again, stillKnown := p.FullRecoveryFailure(err)
	if !known || !stillKnown || again.Stage != "claim" || again.Kind != "route_not_ready" {
		t.Error("public extractor exposed mutable shared failure evidence")
	}
	if _, lookupErr := ports.directory.EndpointsByUID(target, "offline"); lookupErr == nil {
		t.Error("failed Claim published an offline proof")
	}
	if closeErr := recovery.Close(ctx); closeErr != nil {
		t.Errorf("caller recovery Close: %v", closeErr)
	}
	if closeErr := slots.Close(ctx); closeErr != nil {
		t.Errorf("slot recovery Close: %v", closeErr)
	}
	if path := os.Getenv("WK_FULL_RECOVERY_ARTIFACT"); path != "" {
		publishFullFailureArtifact(t, path, map[string]any{
			"schema": "wukongim.full_recovery.claim_failure/v1", "passed": !t.Failed(),
			"failure_observed": observed, "stage": stage, "kind": kind,
			"step_elapsed_ns": int64(elapsed), "effective_budget_ns": int64(budget),
			"original_error_family_preserved": errors.Is(err, authority.ErrRouteNotReady),
		})
	}
}

func TestFullRecoveryOwnerAndValidationFailuresRetainTheirOwnStep(t *testing.T) {
	var receipts []map[string]any
	for _, step := range []string{"owner_begin", "owner_page", "owner_end", "validate_before", "validate_after", "directory_begin", "directory_complete", "malformed_page", "directory_install", "directory_end"} {
		t.Run(step, func(t *testing.T) {
			ports, slots, recovery, target := newFullFailureRecovery(t)
			ports.failStep = step
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := recovery.Endpoints(ctx, target, []string{"offline"})
			var diagnostic fullFailureDiagnostic
			observed := errors.As(err, &diagnostic)
			stage, kind := "", ""
			var elapsed, budget time.Duration
			if observed {
				stage, kind, elapsed, budget = diagnostic.FullRecoveryFailure()
			}
			wantKind, wantError := "route_not_ready", authority.ErrRouteNotReady
			wantStage := step
			if step == "validate_after" {
				wantKind, wantError = "stale_generation", authority.ErrStaleGeneration
			} else if step == "directory_begin" {
				wantKind, wantError = "not_leader", authority.ErrNotLeader
			} else if step == "directory_complete" {
				wantKind, wantError = "stale_generation", authority.ErrStaleGeneration
			} else if step == "directory_install" || step == "directory_end" {
				wantKind, wantError = "stale_generation", authority.ErrStaleGeneration
			} else if step == "malformed_page" {
				wantStage, wantKind, wantError = "owner_page", "slot_incomplete", authority.ErrSlotIncomplete
			}
			if !errors.Is(err, wantError) || !observed || stage != wantStage || kind != wantKind || elapsed < 0 || budget <= 0 || budget > 2*time.Second {
				t.Errorf("Full recovery lost failed step: want=%q observed=%t stage=%q kind=%q elapsed=%s budget=%s err=%v", step, observed, stage, kind, elapsed, budget, err)
			}
			if _, lookupErr := ports.directory.EndpointsByUID(target, "offline"); lookupErr == nil {
				t.Error("failed owner/validation published an offline proof")
			}
			if err := recovery.Close(ctx); err != nil {
				t.Error(err)
			}
			if err := slots.Close(ctx); err != nil {
				t.Error(err)
			}
			receipts = append(receipts, map[string]any{"expected_stage": step, "stage": stage, "kind": kind, "observed": observed, "passed": !t.Failed()})
		})
	}
	if path := os.Getenv("WK_FULL_RECOVERY_STEPS_ARTIFACT"); path != "" {
		publishFullFailureArtifact(t, path, map[string]any{"schema": "wukongim.full_recovery.steps/v1", "passed": !t.Failed(), "cases": receipts})
	}
}

func TestFullRecoveryFailureReachesExistingLogFields(t *testing.T) {
	ports, slots, recovery, target := newFullFailureRecovery(t)
	ports.claimFailure = authority.ErrRouteNotReady
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := recovery.Endpoints(ctx, target, []string{"private-uid-not-a-log-field"})
	fields := presencelog.AppendRecoveryProviderFailure([]wklog.Field{wklog.String("existing", "kept")}, err)
	strings := make(map[string]string)
	numbers := make(map[string]int64)
	for _, field := range fields {
		if value, ok := field.Value.(string); ok {
			strings[field.Key] = value
		}
		if value, ok := field.Value.(int64); ok {
			numbers[field.Key] = value
		}
	}
	if len(fields) != 5 || strings["existing"] != "kept" || strings["full_recovery_stage"] != "claim" || strings["full_recovery_kind"] != "route_not_ready" || numbers["full_recovery_step_elapsed_ns"] < 0 || numbers["full_recovery_effective_budget_ns"] <= 0 {
		t.Errorf("existing failure log lost bounded Full evidence: keys=%v numbers=%v", strings, numbers)
	}
	if err := recovery.Close(ctx); err != nil {
		t.Error(err)
	}
	if err := slots.Close(ctx); err != nil {
		t.Error(err)
	}
}

func TestFullRecoveryDeadlineAndSuccessKeepOriginalSemantics(t *testing.T) {
	for _, mode := range []string{"deadline", "caller_canceled", "success"} {
		t.Run(mode, func(t *testing.T) {
			ports, slots, recovery, target := newFullFailureRecovery(t)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if mode == "deadline" {
				ports.claimFailure = context.DeadlineExceeded
			} else if mode == "caller_canceled" {
				cancel()
			}
			routes, err := recovery.Endpoints(ctx, target, []string{"offline"})
			var diagnostic fullFailureDiagnostic
			observed := errors.As(err, &diagnostic)
			switch mode {
			case "deadline":
				if !errors.Is(err, authority.ErrRouteNotReady) || errors.Is(err, context.DeadlineExceeded) || !observed {
					t.Errorf("historical deadline normalization or evidence lost: observed=%t err=%v", observed, err)
				} else if stage, kind, _, _ := diagnostic.FullRecoveryFailure(); stage != "claim" || kind != "deadline" {
					t.Errorf("normalized physical failure changed: %q %q", stage, kind)
				}
			case "caller_canceled":
				if !errors.Is(err, context.Canceled) || observed {
					t.Errorf("caller cancellation invented a physical failure: observed=%t err=%v", observed, err)
				}
			case "success":
				if err != nil || len(routes) != 0 || observed || len(presencelog.AppendRecoveryProviderFailure(nil, err)) != 0 {
					t.Errorf("successful offline proof changed or invented a failure: routes=%d observed=%t err=%v", len(routes), observed, err)
				}
			}
			cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
			defer done()
			if err := recovery.Close(cleanup); err != nil {
				t.Error(err)
			}
			if err := slots.Close(cleanup); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestFullRecoveryOriginalPhysicalBudgetsRemainObserved(t *testing.T) {
	for _, mode := range []string{"chain_deadline", "owner_deadline"} {
		t.Run(mode, func(t *testing.T) {
			ports, slots, recovery, target := newFullFailureRecovery(t)
			wantStage := "claim"
			limit := 2 * time.Second
			if mode == "chain_deadline" {
				ports.failStep = mode
			} else {
				ports.beginDelay, ports.pageDelay = 700*time.Millisecond, 400*time.Millisecond
				wantStage, limit = "owner_page", time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			_, err := recovery.Endpoints(ctx, target, []string{"offline"})
			var diagnostic fullFailureDiagnostic
			if !errors.Is(err, authority.ErrRouteNotReady) || !errors.As(err, &diagnostic) {
				t.Errorf("physical deadline lost original family/receipt: %v", err)
			} else if stage, kind, elapsed, budget := diagnostic.FullRecoveryFailure(); stage != wantStage || kind != "deadline" || elapsed <= 0 || budget <= 0 || budget > limit {
				t.Errorf("physical budget changed: stage=%q kind=%q elapsed=%s budget=%s", stage, kind, elapsed, budget)
			}
			closeFullFailureRecovery(t, recovery, slots)
		})
	}
}

func TestFullRecoveryFailedGroupDoesNotContaminateSibling(t *testing.T) {
	ports, slots, recovery, target := newFullFailureRecovery(t)
	ports.claimFailure, ports.failTarget = authority.ErrRouteNotReady, target.HashSlot
	other := target
	other.HashSlot++
	ports.directory.BecomeAuthority(other)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	results := recovery.EndpointsByTargets(ctx, []p.EndpointLookupGroup{
		{Target: target, UIDs: []string{"failed"}}, {Target: other, UIDs: []string{"offline"}},
	})
	if len(results) != 2 {
		t.Fatal("group alignment changed")
	}
	var first, second fullFailureDiagnostic
	if !errors.Is(results[0].Err, authority.ErrRouteNotReady) || !errors.As(results[0].Err, &first) || results[1].Err != nil || len(results[1].Routes) != 0 || errors.As(results[1].Err, &second) {
		t.Errorf("failed group contaminated sibling: first=%v second=%v", results[0].Err, results[1].Err)
	}
	closeFullFailureRecovery(t, recovery, slots)
}

func TestFullRecoveryParallelBeginFailureJoinsLateSuccessfulCuts(t *testing.T) {
	ports, slots, recovery, target := newFullFailureRecovery(t)
	failed, release := make(chan struct{}), make(chan struct{})
	ports.failOwner, ports.failure = 2, authority.ErrRouteNotReady
	ports.failGate, ports.gate, ports.late = failed, release, true
	ports.entered, ports.canceled = make(chan uint64, 3), make(chan struct{}, 3)
	var failOnce, releaseOnce sync.Once
	unblockFailure := func() { failOnce.Do(func() { close(failed) }) }
	unblockLate := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblockLate)
	t.Cleanup(unblockFailure)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := recovery.Endpoints(ctx, target, []string{"offline"}); done <- err }()
	for range 3 {
		select {
		case <-ports.entered:
		case <-ctx.Done():
			t.Fatal("parallel Begin providers did not enter")
		}
	}
	unblockFailure()
	for range 2 {
		select {
		case <-ports.canceled:
		case <-ctx.Done():
			t.Fatal("failed Begin did not cancel sibling providers")
		}
	}
	select {
	case <-done:
		t.Fatal("returned before joining actual late provider ownership")
	default:
	}
	unblockLate()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		t.Fatal("late providers did not join")
	}
	var diagnostic fullFailureDiagnostic
	if !errors.Is(err, authority.ErrRouteNotReady) || !errors.As(err, &diagnostic) {
		t.Errorf("failed Begin lost original cause/receipt: %v", err)
	} else if stage, kind, _, _ := diagnostic.FullRecoveryFailure(); stage != "owner_begin" || kind != "route_not_ready" {
		t.Errorf("sibling cancellation replaced the actual failed Begin: %q %q", stage, kind)
	}
	if ports.aborted.Load() != 2 {
		t.Errorf("late successful owner cuts were not aborted: %d", ports.aborted.Load())
	}
	closeFullFailureRecovery(t, recovery, slots)
}

// Publish only a fully written, synced and closed receipt. A failed close must
// never leave a final-path artifact claiming validation passed.
func publishFullFailureArtifact(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".full-failure-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(append(data, '\n'))
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(f.Name(), path); err != nil {
		t.Fatal(err)
	}
}
