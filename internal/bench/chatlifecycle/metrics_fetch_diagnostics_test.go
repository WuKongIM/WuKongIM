package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

type metricsFetchSourceFunc func(context.Context) (target.MetricsSnapshot, error)

func (f metricsFetchSourceFunc) Metrics(ctx context.Context) (target.MetricsSnapshot, error) {
	return f(ctx)
}

type metricsFetchTripFunc func(*http.Request) (*http.Response, error)

func (f metricsFetchTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type metricsFetchSecretError struct {
	cause     error
	formatted atomic.Int32
}

func (e *metricsFetchSecretError) Error() string {
	if e != nil {
		e.formatted.Add(1)
	}
	return "private-source-token private-uid http://private.invalid/body"
}
func (e *metricsFetchSecretError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func metricsFetchTargetError(t *testing.T, kind string) error {
	t.Helper()
	secret := &metricsFetchSecretError{}
	client := target.NewClient(target.Config{APIAddrs: []string{"http://node.invalid"}, HTTPClient: &http.Client{Transport: metricsFetchTripFunc(func(req *http.Request) (*http.Response, error) {
		if kind == "transport" {
			return nil, secret
		}
		status := 200
		body := "go_goroutines NaN\n"
		if kind == "auth" {
			status = 403
			body = "private-source-token"
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: req}, nil
	})}})
	_, err := client.Metrics(context.Background())
	if err == nil {
		t.Fatal("fixture requires failed Metrics")
	}
	return err
}

func metricsFetchProjection(t *testing.T, err error) map[string]any {
	t.Helper()
	failure := finalizationDiagnostic(metaCheckpointFailure("metrics_fetch", err), "controller_unknown", "invalid")
	raw, marshalErr := json.Marshal(failure)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	var got map[string]any
	if json.Unmarshal(raw, &got) != nil {
		t.Fatal("cannot decode safe projection")
	}
	if got["stage"] != "meta_checkpoint" || got["reason"] != "metrics_fetch" {
		t.Errorf("outer leaf changed: %s", raw)
	}
	return got
}
func metricsFetchAssertVector(t *testing.T, record map[string]any, mask int, kinds [3]string) {
	t.Helper()
	nested, ok := record["metrics_fetch"].(map[string]any)
	if !ok {
		t.Error("missing bounded metrics_fetch projection")
		return
	}
	if nested["source_mask"] != float64(mask) {
		t.Errorf("source mask=%v want=%d", nested["source_mask"], mask)
	}
	want := []any{kinds[0], kinds[1], kinds[2]}
	if !reflect.DeepEqual(nested["kinds"], want) {
		t.Errorf("closed kinds=%v want=%v", nested["kinds"], want)
	}
	if len(nested) != 2 {
		t.Error("metrics fetch projection has unbounded fields")
	}
}

func TestMetricsFetchThreeSourceJoinAndOrder(t *testing.T) {
	permutations := [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	typed := [3]error{metricsFetchTargetError(t, "transport"), metricsFetchTargetError(t, "parse"), metricsFetchTargetError(t, "auth")}
	tags := [3]string{"transport", "parse", "auth"}
	for mask := 0; mask < 8; mask++ {
		for orderID, order := range permutations {
			t.Run(fmt.Sprintf("mask_%d_order_%d", mask, orderID), func(t *testing.T) {
				started := make(chan int, 3)
				returned := make(chan int, 3)
				var release [3]chan struct{}
				var calls atomic.Int32
				var sources [3]ProductionMetaMetricsSource
				var errs [3]error
				var kinds [3]string
				for i := range sources {
					release[i] = make(chan struct{})
					if mask&(1<<i) != 0 {
						errs[i] = typed[i]
						kinds[i] = tags[i]
					}
					sources[i] = metricsFetchSourceFunc(func(context.Context) (target.MetricsSnapshot, error) {
						calls.Add(1)
						started <- i
						<-release[i]
						returned <- i
						return target.MetricsSnapshot{GoGoroutines: float64(i + 1)}, errs[i]
					})
				}
				type outcome struct {
					snapshots [3]target.MetricsSnapshot
					err       error
				}
				done := make(chan outcome, 1)
				go func() {
					snapshots, err := collectProductionMetaMetrics(context.Background(), sources)
					done <- outcome{snapshots, err}
				}()
				for range 3 {
					<-started
				}
				close(release[order[0]])
				<-returned
				var early *outcome
				select {
				case got := <-done:
					early = &got
					t.Error("collection returned before joining all sources")
				default:
				}
				close(release[order[1]])
				close(release[order[2]])
				for range 2 {
					<-returned
				}
				var got outcome
				if early != nil {
					got = *early
				} else {
					got = <-done
				}
				if calls.Load() != 3 {
					t.Error("source call count changed")
				}
				if mask == 0 {
					if got.err != nil {
						t.Fatal("successful source round failed")
					}
					for i := range got.snapshots {
						if got.snapshots[i].GoGoroutines != float64(i+1) {
							t.Error("completion order reordered source snapshot")
						}
					}
					return
				}
				if !errors.Is(got.err, ErrLifecycleHarnessInvalid) {
					t.Fatal("failed source round lost harness classification")
				}
				if !reflect.DeepEqual(got.snapshots, [3]target.MetricsSnapshot{}) {
					t.Error("partial snapshots escaped failed round")
				}
				metricsFetchAssertVector(t, metricsFetchProjection(t, got.err), mask, kinds)
			})
		}
	}
}

func TestMetricsFetchPrivateCauseIsolation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		kind  string
	}{
		{"private", nil, "unknown"}, {"canceled", context.Canceled, "context_canceled"}, {"deadline", context.DeadlineExceeded, "context_deadline"},
		{"product", ErrLifecycleProductFailure, "unknown"}, {"both_contexts", errors.Join(context.Canceled, context.DeadlineExceeded), "mixed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			private := &metricsFetchSecretError{cause: tc.cause}
			f := newMetaDiagnosticFixture(t, true)
			f.sources[1].err = private
			before := f.accounting.Snapshot()
			waits := 0
			f.controller.settleWait = func(context.Context) error { waits++; return nil }
			err := f.controller.Checkpoint(context.Background(), f.workers, f.assignment, true)
			if !errors.Is(err, ErrLifecycleHarnessInvalid) {
				t.Fatal("lost harness sentinel")
			}
			for _, hidden := range []error{private, context.Canceled, context.DeadlineExceeded, ErrLifecycleProductFailure} {
				if errors.Is(err, hidden) {
					t.Error("private source cause changed aggregate classification")
				}
			}
			if waits != 0 || !reflect.DeepEqual(before, f.accounting.Snapshot()) {
				t.Error("failed fetch changed settle retries or baseline")
			}
			for _, source := range f.sources {
				if source.calls != 2 {
					t.Error("fetch failure added a retry or skipped a source")
				}
			}
			metricsFetchAssertVector(t, metricsFetchProjection(t, err), 2, [3]string{"", tc.kind, ""})
			if strings.Contains(fmt.Sprint(err), "private") || strings.Contains(fmt.Sprintf("%+v", err), "private") || private.formatted.Load() != 0 {
				t.Error("source cause was formatted")
			}
			// The aggregate must retain exactly the original three references privately,
			// while its only unwrapped public cause remains the old harness sentinel.
			aggregate := errors.Unwrap(err)
			value := reflect.ValueOf(aggregate)
			if value.Kind() != reflect.Pointer || value.IsNil() {
				t.Error("missing private aggregate")
				return
			}
			field := value.Elem().FieldByName("causes")
			if !field.IsValid() || field.Type() != reflect.TypeOf([3]error{}) {
				t.Error("private causes are not a fixed three-element array")
				return
			}
			if !field.Index(0).IsNil() || !field.Index(2).IsNil() || field.Index(1).IsNil() || field.Index(1).Elem().Pointer() != reflect.ValueOf(private).Pointer() {
				t.Error("aggregate did not retain exact source references")
			}
			if errors.Unwrap(aggregate) != ErrLifecycleHarnessInvalid {
				t.Error("aggregate unwrap exposed source errors")
			}
		})
	}
}

func TestMetricsFetchParentContextStillWinsAfterJoin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 3)
	returned := make(chan struct{}, 3)
	release := make(chan struct{})
	var sources [3]ProductionMetaMetricsSource
	for i := range sources {
		sources[i] = metricsFetchSourceFunc(func(context.Context) (target.MetricsSnapshot, error) {
			started <- struct{}{}
			<-release
			returned <- struct{}{}
			return target.MetricsSnapshot{}, &metricsFetchSecretError{}
		})
	}
	done := make(chan error, 1)
	go func() { _, err := collectProductionMetaMetrics(ctx, sources); done <- err }()
	for range 3 {
		<-started
	}
	cancel()
	select {
	case <-done:
		t.Error("parent cancellation skipped source joins")
	default:
	}
	close(release)
	for range 3 {
		<-returned
	}
	err := <-done
	if err != context.Canceled {
		t.Error("parent context precedence changed")
	}
	failure := finalizationDiagnostic(metaCheckpointFailure("metrics_fetch", err), "controller_unknown", "invalid")
	raw, _ := json.Marshal(failure)
	if failure.Reason != "context_canceled" || strings.Contains(string(raw), "metrics_fetch") {
		t.Error("parent context retained a child-source diagnostic")
	}
}

func TestMetricsFetchParentContextStopsBeforeSources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	var sources [3]ProductionMetaMetricsSource
	for i := range sources {
		sources[i] = metricsFetchSourceFunc(func(context.Context) (target.MetricsSnapshot, error) { calls++; return target.MetricsSnapshot{}, nil })
	}
	_, err := collectProductionMetaMetrics(ctx, sources)
	if err != context.Canceled || calls != 0 {
		t.Error("pre-canceled round contacted sources")
	}
}

func TestMetricsFetchBoundedProjection(t *testing.T) {
	cases := []struct {
		name          string
		stage, reason string
		mask          int
		kinds         [3]string
		want          bool
		wantKinds     [3]string
	}{
		{name: "valid", stage: "meta_checkpoint", reason: "metrics_fetch", mask: 5, kinds: [3]string{"transport", "", "parse"}, want: true, wantKinds: [3]string{"transport", "", "parse"}},
		{name: "malicious_kind", stage: "meta_checkpoint", reason: "metrics_fetch", mask: 1, kinds: [3]string{strings.Repeat("private-token", 1024), "", ""}, want: true, wantKinds: [3]string{"unknown", "", ""}},
		{name: "zero_mask", stage: "meta_checkpoint", reason: "metrics_fetch"},
		{name: "outside_mask", stage: "meta_checkpoint", reason: "metrics_fetch", mask: 8, kinds: [3]string{"transport", "", ""}},
		{name: "missing_failure_kind", stage: "meta_checkpoint", reason: "metrics_fetch", mask: 1},
		{name: "success_has_kind", stage: "meta_checkpoint", reason: "metrics_fetch", mask: 1, kinds: [3]string{"transport", "parse", ""}},
		{name: "other_stage", stage: "controller_guard", reason: "state", mask: 1, kinds: [3]string{"transport", "", ""}},
		{name: "other_reason", stage: "meta_checkpoint", reason: "metrics_preview", mask: 1, kinds: [3]string{"transport", "", ""}},
		{name: "unknown_stage", stage: "private-stage", reason: "private-reason", mask: 1, kinds: [3]string{"private-token", "", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{"stage": tc.stage, "reason": tc.reason, "metrics_fetch": map[string]any{"source_mask": tc.mask, "kinds": tc.kinds}}
			raw, _ := json.Marshal(input)
			var failure FinalizationFailure
			if err := json.Unmarshal(raw, &failure); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(failure)
			bounded := failure.Bounded()
			after, _ := json.Marshal(failure)
			if string(before) != string(after) {
				t.Error("Bounded mutated caller-owned data")
			}
			encoded, _ := json.Marshal(bounded)
			var got map[string]any
			_ = json.Unmarshal(encoded, &got)
			if tc.want {
				metricsFetchAssertVector(t, got, tc.mask, tc.wantKinds)
			} else if _, exists := got["metrics_fetch"]; exists {
				t.Error("invalid/foreign metrics projection escaped")
			}
			if strings.Contains(string(encoded), "private") {
				t.Error("private injected data escaped Bounded")
			}
		})
	}
	for mask := 0; mask < 256; mask++ {
		t.Run(fmt.Sprintf("mask_%03d", mask), func(t *testing.T) {
			var kinds [3]string
			for i := range 3 {
				if mask&(1<<i) != 0 {
					kinds[i] = "unknown"
				}
			}
			raw, _ := json.Marshal(map[string]any{"stage": "meta_checkpoint", "reason": "metrics_fetch", "metrics_fetch": map[string]any{"source_mask": mask, "kinds": kinds}})
			var f FinalizationFailure
			_ = json.Unmarshal(raw, &f)
			encoded, _ := json.Marshal(f.Bounded())
			var got map[string]any
			_ = json.Unmarshal(encoded, &got)
			if mask > 0 && mask < 8 {
				metricsFetchAssertVector(t, got, mask, kinds)
			} else if _, ok := got["metrics_fetch"]; ok {
				t.Error("invalid mask retained")
			}
		})
	}
}

func TestMetricsFetchFinalizePreservesFailureAndNoReport(t *testing.T) {
	f := newMetaDiagnosticFixture(t, false)
	f.sources[0].err = &metricsFetchSecretError{cause: ErrLifecycleProductFailure}
	metaErr := f.controller.Checkpoint(context.Background(), f.workers, f.assignment, false)
	c, cut, log, _ := newFinalizeDiagnosticFixture(t)
	before := c.frozen
	c.meta = finalizeMetaProbe{err: metaErr}
	err := c.Finalize(context.Background(), cut)
	if err == nil || !errors.Is(err, ErrLifecycleHarnessInvalid) || errors.Is(err, ErrLifecycleProductFailure) {
		t.Fatal("source product error became product evidence")
	}
	if !reflect.DeepEqual(c.frozen, before) {
		t.Error("frozen evidence changed")
	}
	if _, err := os.Stat(filepath.Join(c.outputDir, "final.json")); !os.IsNotExist(err) {
		t.Error("failed metrics fetch produced final report")
	}
	found := 0
	for _, line := range strings.Split(log.String(), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil {
			t.Fatal("invalid diagnostic JSON")
		}
		if record["event"] == "wkbench.chat_lifecycle.controller_failure" {
			found++
			metricsFetchAssertVector(t, record, 1, [3]string{"unknown", "", ""})
			if record["reason"] != "metrics_fetch" || record["observed_outcome"] != string(before.Outcome) {
				t.Error("finalization leaf or frozen observation changed")
			}
		}
	}
	if found != 1 || strings.Contains(log.String(), "private") {
		t.Error("missing, duplicate or unsafe failure event")
	}
}

// A custom source cannot introduce arbitrary public kinds through a method name.
type metricsFetchSpoofError struct{}

func (*metricsFetchSpoofError) Error() string              { return "private-spoof-token" }
func (*metricsFetchSpoofError) MetricsFailureKind() string { return "private-spoof-token" }

func TestMetricsFetchUnknownSourceErrorShapes(t *testing.T) {
	var typedNil *metricsFetchSecretError
	for _, tc := range []struct {
		name string
		err  error
		kind string
	}{
		{"typed_nil", typedNil, "unknown"}, {"wrapped", errors.Join(&metricsFetchSecretError{}), "unknown"}, {"spoof_method", &metricsFetchSpoofError{}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMetaDiagnosticFixture(t, false)
			f.sources[2].err = tc.err
			err := f.controller.Checkpoint(context.Background(), f.workers, f.assignment, false)
			metricsFetchAssertVector(t, metricsFetchProjection(t, err), 4, [3]string{"", "", tc.kind})
		})
	}
}

func TestMetricsFetchConstructorStillRejectsMissingSource(t *testing.T) {
	_, err := NewProductionMetaController(ProductionMetaControllerOptions{Config: LocalConfig(), Accounting: NewMetaCreateAccounting()})
	if err != ErrLifecycleHarnessInvalid {
		t.Error("constructor guard changed")
	}
}

func TestMetricsFetchSameKindSourceMasks(t *testing.T) {
	typed := metricsFetchTargetError(t, "transport")
	for _, mask := range []int{3, 5, 6, 7} {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			f := newMetaDiagnosticFixture(t, false)
			var kinds [3]string
			for i := range f.sources {
				if mask&(1<<i) != 0 {
					f.sources[i].err = typed
					kinds[i] = "transport"
				}
			}
			err := f.controller.Checkpoint(context.Background(), f.workers, f.assignment, false)
			metricsFetchAssertVector(t, metricsFetchProjection(t, err), mask, kinds)
		})
	}
}

// metricsFetchCutContext publishes a deterministic terminal context without a timer.
type metricsFetchCutContext struct {
	context.Context
	done  chan struct{}
	cause error
}

func (c *metricsFetchCutContext) Done() <-chan struct{} { return c.done }
func (c *metricsFetchCutContext) Err() error {
	select {
	case <-c.done:
		return c.cause
	default:
		return nil
	}
}
func (c *metricsFetchCutContext) stop(err error) { c.cause = err; close(c.done) }

func TestMetricsFetchParentDeadlineBeforeAndAfterJoin(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(fmt.Sprint(before), func(t *testing.T) {
			ctx := &metricsFetchCutContext{Context: context.Background(), done: make(chan struct{})}
			started := make(chan struct{}, 3)
			returned := make(chan struct{}, 3)
			release := make(chan struct{})
			var calls atomic.Int32
			var sources [3]ProductionMetaMetricsSource
			for i := range sources {
				sources[i] = metricsFetchSourceFunc(func(context.Context) (target.MetricsSnapshot, error) {
					calls.Add(1)
					started <- struct{}{}
					<-release
					returned <- struct{}{}
					return target.MetricsSnapshot{}, &metricsFetchSecretError{}
				})
			}
			if before {
				ctx.stop(context.DeadlineExceeded)
				_, err := collectProductionMetaMetrics(ctx, sources)
				if err != context.DeadlineExceeded || calls.Load() != 0 {
					t.Error("expired parent contacted sources")
				}
				return
			}
			done := make(chan error, 1)
			go func() { _, err := collectProductionMetaMetrics(ctx, sources); done <- err }()
			for range 3 {
				<-started
			}
			ctx.stop(context.DeadlineExceeded)
			select {
			case <-done:
				t.Error("deadline skipped joins")
			default:
			}
			close(release)
			for range 3 {
				<-returned
			}
			err := <-done
			if err != context.DeadlineExceeded {
				t.Error("parent deadline precedence lost")
			}
			failure := finalizationDiagnostic(metaCheckpointFailure("metrics_fetch", err), "controller_unknown", "invalid")
			raw, _ := json.Marshal(failure)
			if failure.Reason != "context_deadline" || strings.Contains(string(raw), "metrics_fetch") {
				t.Error("parent deadline became source diagnostic")
			}
		})
	}
}
