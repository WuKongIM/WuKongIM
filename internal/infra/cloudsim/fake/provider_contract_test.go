package fake

import (
	"context"
	"errors"
	"maps"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/cloudsim"
)

func TestProviderIdentityInventoryAndQuoteContracts(t *testing.T) {
	now := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
	quote := cloudsim.Quote{
		Currency:               "USD",
		WorstCaseCostMicros:    12_500_000,
		SelectedSKU:            "fake.contract",
		SpotPriceMicrosPerHour: 750_000,
		CapacityAvailable:      true,
		QuotaAvailable:         true,
	}
	provider := New(Options{Now: func() time.Time { return now }, Quote: quote})
	if got := provider.Name(); got != ProviderName {
		t.Fatalf("Name() = %q, want %q", got, ProviderName)
	}
	if _, err := provider.Authority(context.Background()); !errors.Is(err, ErrStateStore) {
		t.Fatalf("Authority() on empty inventory error = %v, want ErrStateStore", err)
	}
	if got, err := provider.Quote(context.Background(), cloudsim.CreateRequest{}); err != nil || got != quote {
		t.Fatalf("Quote() = (%#v, %v), want (%#v, nil)", got, err, quote)
	}

	for _, runID := range []string{"run-z", "run-a"} {
		createFakeRun(t, provider, fakeRequestForRun(now, runID, "cn-test", "sha256:account"))
	}
	authority, err := provider.Authority(context.Background())
	if err != nil {
		t.Fatalf("Authority() error = %v", err)
	}
	wantAuthority := cloudsim.ProviderAuthority{
		Provider: ProviderName, Region: "cn-test", AccountIDHash: "sha256:account",
	}
	if authority != wantAuthority {
		t.Fatalf("Authority() = %#v, want %#v", authority, wantAuthority)
	}

	runs, err := provider.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if got := []string{runs[0].ID, runs[1].ID}; !reflect.DeepEqual(got, []string{"run-a", "run-z"}) {
		t.Fatalf("Inventory() order = %v, want lexical run identity order", got)
	}
	runs[0].Tags[cloudsim.TagRunID] = "mutated"
	runs[0].Resources[0].Tags[cloudsim.TagRunID] = "mutated"
	runsAgain, err := provider.Inventory(context.Background())
	if err != nil {
		t.Fatalf("second Inventory() error = %v", err)
	}
	if runsAgain[0].Tags[cloudsim.TagRunID] != "run-a" ||
		runsAgain[0].Resources[0].Tags[cloudsim.TagRunID] != "run-a" {
		t.Fatalf("Inventory() exposed mutable provider state: %#v", runsAgain[0])
	}

	conflicting := fakeRequestForRun(now, "run-other-authority", "cn-other", "sha256:other-account")
	createFakeRun(t, provider, conflicting)
	if _, err := provider.Authority(context.Background()); !errors.Is(err, ErrStateStore) {
		t.Fatalf("Authority() with mixed authorities error = %v, want ErrStateStore", err)
	}
}

func TestProviderFailureInjectionAndOptionOwnership(t *testing.T) {
	now := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
	failures := map[string]bool{"run-protected": true}
	provider := New(Options{
		Now: func() time.Time { return now },
		Failures: FailurePlan{
			Inventory:     true,
			Quote:         true,
			DestroyRunIDs: failures,
		},
	})
	createFakeRunWithQuote(t, provider, fakeRequestForRun(now, "run-protected", "cn-test", "sha256:account"), cloudsim.Quote{})

	// Options are caller-owned. Mutating them after construction must not alter
	// the provider's deterministic failure plan.
	delete(failures, "run-protected")
	if _, err := provider.Inventory(context.Background()); !errors.Is(err, ErrInjectedFailure) {
		t.Fatalf("Inventory() error = %v, want ErrInjectedFailure", err)
	}
	if _, err := provider.Quote(context.Background(), cloudsim.CreateRequest{}); !errors.Is(err, ErrInjectedFailure) {
		t.Fatalf("Quote() error = %v, want ErrInjectedFailure", err)
	}
	if _, err := provider.Destroy(context.Background(), "run-protected"); !errors.Is(err, ErrInjectedFailure) {
		t.Fatalf("Destroy() error = %v, want snapshotted ErrInjectedFailure", err)
	}

	lateFailures := map[string]bool{}
	lateProvider := New(Options{
		Now:      func() time.Time { return now },
		Failures: FailurePlan{DestroyRunIDs: lateFailures},
	})
	createFakeRun(t, lateProvider, fakeRequestForRun(now, "run-late", "cn-test", "sha256:account"))
	lateFailures["run-late"] = true
	if _, err := lateProvider.Destroy(context.Background(), "run-late"); err != nil {
		t.Fatalf("Destroy() inherited a caller mutation after New(): %v", err)
	}
}

func TestProviderCreateValidationIdempotencyAndCopyIsolation(t *testing.T) {
	now := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
	for _, key := range cloudsim.MandatoryTagKeys() {
		t.Run(key, func(t *testing.T) {
			provider := New(Options{Now: func() time.Time { return now }})
			req := fakeRequestForRun(now, "run-invalid", "cn-test", "sha256:account")
			delete(req.Tags, key)
			quote, err := provider.Quote(context.Background(), req)
			if err != nil {
				t.Fatalf("Quote() error = %v", err)
			}
			if _, err := provider.Create(context.Background(), req, quote); !errors.Is(err, ErrInvalidTags) {
				t.Fatalf("Create() error = %v, want ErrInvalidTags", err)
			}
			if _, err := provider.Status(context.Background(), req.RunID); !errors.Is(err, cloudsim.ErrRunNotFound) {
				t.Fatalf("invalid Create() left inventory, Status error = %v", err)
			}
		})
	}

	provider := New(Options{Now: func() time.Time { return now }})
	req := fakeRequestForRun(now, "run-copy", "cn-test", "sha256:account")
	run := createFakeRun(t, provider, req)
	if _, err := provider.Create(context.Background(), req, run.Quote); !errors.Is(err, cloudsim.ErrActiveRunExists) {
		t.Fatalf("duplicate Create() error = %v, want ErrActiveRunExists", err)
	}

	req.Tags[cloudsim.TagRepository] = "caller/mutated"
	run.Tags[cloudsim.TagRepository] = "return/mutated"
	run.Resources[0].Tags[cloudsim.TagRepository] = "resource/mutated"
	stored, err := provider.Status(context.Background(), req.RunID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if got := stored.Tags[cloudsim.TagRepository]; got != "WuKongIM/WuKongIM" {
		t.Fatalf("stored run repository tag = %q, want immutable request snapshot", got)
	}
	if got := stored.Resources[0].Tags[cloudsim.TagRepository]; got != "WuKongIM/WuKongIM" {
		t.Fatalf("stored resource repository tag = %q, want immutable request snapshot", got)
	}

	if _, err := provider.Destroy(context.Background(), req.RunID); err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	recreatedReq := fakeRequestForRun(now, req.RunID, "cn-test", "sha256:account")
	recreated := createFakeRun(t, provider, recreatedReq)
	if recreated.State != cloudsim.StateReady || len(recreated.Resources) != 12 {
		t.Fatalf("Create() after a proven release = %#v, want a fresh ready run", recreated)
	}
}

func TestProviderLifecycleWindowsAndRelease(t *testing.T) {
	now := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
	current := now
	provider := New(Options{
		Now: func() time.Time { return current },
		Failures: FailurePlan{DestroyRunIDs: map[string]bool{
			"run-destroy-fails": true,
		}},
	})
	req := fakeRequestForRun(now, "run-life", "cn-test", "sha256:account")
	createFakeRun(t, provider, req)

	if _, err := provider.Transition(context.Background(), cloudsim.TransitionRequest{RunID: "missing"}); !errors.Is(err, cloudsim.ErrRunNotFound) {
		t.Fatalf("Transition(missing) error = %v, want ErrRunNotFound", err)
	}
	activeUntil := now.Add(25 * time.Minute)
	run, err := provider.Transition(context.Background(), cloudsim.TransitionRequest{
		RunID: req.RunID, Next: cloudsim.StateRunning, ActiveUntil: activeUntil,
	})
	if err != nil {
		t.Fatalf("Transition(running) error = %v", err)
	}
	if run.State != cloudsim.StateRunning || !run.ActiveUntil.Equal(activeUntil) {
		t.Fatalf("Transition(running) = %#v", run)
	}
	current = activeUntil
	status, err := provider.Status(context.Background(), req.RunID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != cloudsim.StateAnalysisGrace {
		t.Fatalf("Status() at ActiveUntil state = %q, want analysis_grace", status.State)
	}

	deploymentPrefix := netip.MustParsePrefix("192.0.2.10/32")
	analysisPrefix := netip.MustParsePrefix("198.51.100.20/32")
	publicPrefix := netip.MustParsePrefix("0.0.0.0/0")
	deploymentUntil := now.Add(10 * time.Minute)
	analysisUntil := now.Add(40 * time.Minute)
	publicUntil := now.Add(time.Hour)
	run, err = provider.OpenDeployment(context.Background(), cloudsim.OpenDeploymentRequest{
		RunID: req.RunID, SourcePrefix: deploymentPrefix, Until: deploymentUntil,
	})
	if err != nil || run.DeploymentWindow == nil || run.DeploymentWindow.SourcePrefix != deploymentPrefix || !run.DeploymentWindow.Until.Equal(deploymentUntil) {
		t.Fatalf("OpenDeployment() = (%#v, %v)", run.DeploymentWindow, err)
	}
	run, err = provider.OpenAnalysis(context.Background(), cloudsim.OpenAnalysisRequest{
		RunID: req.RunID, SourcePrefix: analysisPrefix, Until: analysisUntil,
	})
	if err != nil || run.AnalysisWindow == nil || run.AnalysisWindow.SourcePrefix != analysisPrefix || !run.AnalysisWindow.Until.Equal(analysisUntil) {
		t.Fatalf("OpenAnalysis() = (%#v, %v)", run.AnalysisWindow, err)
	}
	run, err = provider.OpenPublicView(context.Background(), cloudsim.OpenPublicViewRequest{
		RunID: req.RunID, SourcePrefix: publicPrefix, Until: publicUntil,
	})
	if err != nil || run.PublicViewWindow == nil || run.PublicViewWindow.SourcePrefix != publicPrefix || !run.PublicViewWindow.Until.Equal(publicUntil) {
		t.Fatalf("OpenPublicView() = (%#v, %v)", run.PublicViewWindow, err)
	}

	// Window pointers returned to callers must not alias the stored run.
	run.DeploymentWindow.Until = time.Time{}
	run.AnalysisWindow.Until = time.Time{}
	run.PublicViewWindow.Until = time.Time{}
	stored, err := provider.Status(context.Background(), req.RunID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !stored.DeploymentWindow.Until.Equal(deploymentUntil) ||
		!stored.AnalysisWindow.Until.Equal(analysisUntil) ||
		!stored.PublicViewWindow.Until.Equal(publicUntil) {
		t.Fatalf("window return values alias stored state: %#v", stored)
	}

	closeCalls := []struct {
		name string
		call func() (cloudsim.Run, error)
	}{
		{name: "deployment", call: func() (cloudsim.Run, error) { return provider.CloseDeployment(context.Background(), req.RunID) }},
		{name: "analysis", call: func() (cloudsim.Run, error) { return provider.CloseAnalysis(context.Background(), req.RunID) }},
		{name: "public view", call: func() (cloudsim.Run, error) { return provider.ClosePublicView(context.Background(), req.RunID) }},
	}
	for _, item := range closeCalls {
		if _, err := item.call(); err != nil {
			t.Fatalf("Close%s() error = %v", item.name, err)
		}
	}
	closed, err := provider.Status(context.Background(), req.RunID)
	if err != nil {
		t.Fatalf("Status() after closes error = %v", err)
	}
	if closed.DeploymentWindow != nil || closed.AnalysisWindow != nil || closed.PublicViewWindow != nil {
		t.Fatalf("close operations left ingress windows: %#v", closed)
	}

	released, err := provider.Destroy(context.Background(), req.RunID)
	if err != nil {
		t.Fatalf("Destroy() error = %v", err)
	}
	if released.State != cloudsim.StateReleased || len(released.Resources) != 0 {
		t.Fatalf("Destroy() = %#v, want released tombstone", released)
	}
	if _, err := provider.Destroy(context.Background(), req.RunID); err != nil {
		t.Fatalf("idempotent Destroy() error = %v", err)
	}
	assertReleasedOpenErrors(t, provider, req.RunID, now)

	failingReq := fakeRequestForRun(now, "run-destroy-fails", "cn-test", "sha256:account")
	createFakeRun(t, provider, failingReq)
	if _, err := provider.Destroy(context.Background(), failingReq.RunID); !errors.Is(err, ErrInjectedFailure) {
		t.Fatalf("Destroy(failure plan) error = %v, want ErrInjectedFailure", err)
	}
	if status, err := provider.Status(context.Background(), failingReq.RunID); err != nil || status.State != cloudsim.StateReady {
		t.Fatalf("failed Destroy() changed run = (%#v, %v)", status, err)
	}
}

func TestProviderMissingRunOperations(t *testing.T) {
	provider := New(Options{})
	now := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
	calls := []struct {
		name string
		call func() error
	}{
		{name: "status", call: func() error { _, err := provider.Status(context.Background(), "missing"); return err }},
		{name: "open deployment", call: func() error {
			_, err := provider.OpenDeployment(context.Background(), cloudsim.OpenDeploymentRequest{RunID: "missing", Until: now})
			return err
		}},
		{name: "close deployment", call: func() error { _, err := provider.CloseDeployment(context.Background(), "missing"); return err }},
		{name: "open analysis", call: func() error {
			_, err := provider.OpenAnalysis(context.Background(), cloudsim.OpenAnalysisRequest{RunID: "missing", Until: now})
			return err
		}},
		{name: "close analysis", call: func() error { _, err := provider.CloseAnalysis(context.Background(), "missing"); return err }},
		{name: "open public view", call: func() error {
			_, err := provider.OpenPublicView(context.Background(), cloudsim.OpenPublicViewRequest{RunID: "missing", Until: now})
			return err
		}},
		{name: "close public view", call: func() error { _, err := provider.ClosePublicView(context.Background(), "missing"); return err }},
		{name: "destroy", call: func() error { _, err := provider.Destroy(context.Background(), "missing"); return err }},
	}
	for _, item := range calls {
		t.Run(item.name, func(t *testing.T) {
			if err := item.call(); !errors.Is(err, cloudsim.ErrRunNotFound) {
				t.Fatalf("operation error = %v, want ErrRunNotFound", err)
			}
		})
	}
}

func TestProviderCanceledCallsDoNotReadOrMutateInventory(t *testing.T) {
	now := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
	provider := New(Options{Now: func() time.Time { return now }})
	req := fakeRequestForRun(now, "run-cancel", "cn-test", "sha256:account")
	createFakeRun(t, provider, req)
	before, err := provider.Status(context.Background(), req.RunID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	newReq := fakeRequestForRun(now, "run-canceled-create", "cn-test", "sha256:account")
	calls := []struct {
		name string
		call func() error
	}{
		{name: "authority", call: func() error { _, err := provider.Authority(ctx); return err }},
		{name: "inventory", call: func() error { _, err := provider.Inventory(ctx); return err }},
		{name: "quote", call: func() error { _, err := provider.Quote(ctx, req); return err }},
		{name: "create", call: func() error { _, err := provider.Create(ctx, newReq, before.Quote); return err }},
		{name: "status", call: func() error { _, err := provider.Status(ctx, req.RunID); return err }},
		{name: "transition", call: func() error {
			_, err := provider.Transition(ctx, cloudsim.TransitionRequest{RunID: req.RunID, Next: cloudsim.StateRunning, ActiveUntil: now.Add(time.Minute)})
			return err
		}},
		{name: "open deployment", call: func() error {
			_, err := provider.OpenDeployment(ctx, cloudsim.OpenDeploymentRequest{RunID: req.RunID, Until: now.Add(time.Minute)})
			return err
		}},
		{name: "close deployment", call: func() error { _, err := provider.CloseDeployment(ctx, req.RunID); return err }},
		{name: "open analysis", call: func() error {
			_, err := provider.OpenAnalysis(ctx, cloudsim.OpenAnalysisRequest{RunID: req.RunID, Until: now.Add(time.Minute)})
			return err
		}},
		{name: "close analysis", call: func() error { _, err := provider.CloseAnalysis(ctx, req.RunID); return err }},
		{name: "open public view", call: func() error {
			_, err := provider.OpenPublicView(ctx, cloudsim.OpenPublicViewRequest{RunID: req.RunID, Until: now.Add(time.Minute)})
			return err
		}},
		{name: "close public view", call: func() error { _, err := provider.ClosePublicView(ctx, req.RunID); return err }},
		{name: "destroy", call: func() error { _, err := provider.Destroy(ctx, req.RunID); return err }},
	}
	for _, item := range calls {
		t.Run(item.name, func(t *testing.T) {
			if err := item.call(); !errors.Is(err, context.Canceled) {
				t.Fatalf("operation error = %v, want context.Canceled", err)
			}
		})
	}
	after, err := provider.Status(context.Background(), req.RunID)
	if err != nil {
		t.Fatalf("Status() after canceled calls error = %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("canceled calls mutated run\nbefore: %#v\nafter:  %#v", before, after)
	}
	if _, err := provider.Status(context.Background(), newReq.RunID); !errors.Is(err, cloudsim.ErrRunNotFound) {
		t.Fatalf("canceled Create() left inventory, Status error = %v", err)
	}
}

func TestProviderConcurrentRunIsolation(t *testing.T) {
	now := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
	provider := New(Options{Now: func() time.Time { return now }})
	const runCount = 32
	start := make(chan struct{})
	errs := make(chan error, runCount)
	var workers sync.WaitGroup
	workers.Add(runCount)
	for index := 0; index < runCount; index++ {
		index := index
		go func() {
			defer workers.Done()
			<-start
			runID := "run-concurrent-" + twoDigits(index)
			req := fakeRequestForRun(now, runID, "cn-test", "sha256:account")
			quote, err := provider.Quote(context.Background(), req)
			if err != nil {
				errs <- err
				return
			}
			run, err := provider.Create(context.Background(), req, quote)
			if err != nil {
				errs <- err
				return
			}
			run.Tags[cloudsim.TagRunID] = "worker-mutated"
			status, err := provider.Status(context.Background(), runID)
			if err != nil {
				errs <- err
				return
			}
			if status.Tags[cloudsim.TagRunID] != runID {
				errs <- errors.New("concurrent return value aliased stored state")
			}
		}()
	}
	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent provider operation error = %v", err)
		}
	}

	runs, err := provider.Inventory(context.Background())
	if err != nil {
		t.Fatalf("Inventory() error = %v", err)
	}
	if len(runs) != runCount {
		t.Fatalf("Inventory() count = %d, want %d", len(runs), runCount)
	}
	for index, run := range runs {
		wantID := "run-concurrent-" + twoDigits(index)
		if run.ID != wantID {
			t.Fatalf("Inventory()[%d].ID = %q, want %q", index, run.ID, wantID)
		}
	}
}

func assertReleasedOpenErrors(t *testing.T, provider *Provider, runID string, now time.Time) {
	t.Helper()
	calls := []struct {
		name string
		call func() error
	}{
		{name: "deployment", call: func() error {
			_, err := provider.OpenDeployment(context.Background(), cloudsim.OpenDeploymentRequest{RunID: runID, Until: now.Add(time.Minute)})
			return err
		}},
		{name: "analysis", call: func() error {
			_, err := provider.OpenAnalysis(context.Background(), cloudsim.OpenAnalysisRequest{RunID: runID, Until: now.Add(time.Minute)})
			return err
		}},
		{name: "public view", call: func() error {
			_, err := provider.OpenPublicView(context.Background(), cloudsim.OpenPublicViewRequest{RunID: runID, Until: now.Add(time.Minute)})
			return err
		}},
	}
	for _, item := range calls {
		if err := item.call(); !errors.Is(err, cloudsim.ErrRunReleased) {
			t.Fatalf("Open %s on released run error = %v, want ErrRunReleased", item.name, err)
		}
	}
}

func createFakeRun(t *testing.T, provider *Provider, req cloudsim.CreateRequest) cloudsim.Run {
	t.Helper()
	quote, err := provider.Quote(context.Background(), req)
	if err != nil {
		t.Fatalf("Quote(%s) error = %v", req.RunID, err)
	}
	return createFakeRunWithQuote(t, provider, req, quote)
}

func createFakeRunWithQuote(t *testing.T, provider *Provider, req cloudsim.CreateRequest, quote cloudsim.Quote) cloudsim.Run {
	t.Helper()
	run, err := provider.Create(context.Background(), req, quote)
	if err != nil {
		t.Fatalf("Create(%s) error = %v", req.RunID, err)
	}
	return run
}

func fakeRequestForRun(now time.Time, runID, region, accountIDHash string) cloudsim.CreateRequest {
	req := fakeCreateRequest(now)
	req.RunID = runID
	req.Region = region
	req.AccountIDHash = accountIDHash
	req.Tags = maps.Clone(req.Tags)
	req.Tags[cloudsim.TagRunID] = runID
	return req
}

func twoDigits(value int) string {
	return string([]byte{'0' + byte(value/10), '0' + byte(value%10)})
}
