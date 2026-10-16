//go:build integration

package chatlifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

// Failure cases: earlier script readiness can recover then regress; health
// transport can fail transiently; persistent or blocked probes can wait forever;
// cancellation and authorization failures can be retried; strict config/metrics/
// cluster failures can be hidden by retries; later nodes can be probed only after
// configuration or metrics work has already started. Diagnostics must not retain
// endpoint URLs, credentials, response bodies, or arbitrary errors. Capacity
// continuation must not disguise lost readiness with a fresh startup wait.
func TestPreflightReadinessCapacityContinuationDoesNotRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := FormalConfig()
		cfg.Mode = ModeCapacity
		cfg.Capacity.AgedCheckpoint = AgedCheckpoint{
			Reference: "reports/formal-72h", Completed: true, Passed: true, Duration: 72 * time.Hour,
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("capacity fixture must be valid before readiness: %v", err)
		}
		f := newReadinessFixtureForConfig(cfg, func(_ context.Context, node int, path string, attempt int) (int, error) {
			if node == 0 && path == "/readyz" && attempt == 1 {
				return http.StatusServiceUnavailable, nil
			}
			return http.StatusOK, nil
		})
		started := time.Now()
		result := f.fixture.preflight.Check(context.Background(), cfg)
		if elapsed := time.Since(started); elapsed != 0 {
			t.Errorf("capacity continuation waited %s for readiness recovery", elapsed)
		}
		if calls := f.probeCount(0, "/readyz"); calls != 1 {
			t.Errorf("capacity readiness probes=%d, want one", calls)
		}
		if f.strictCount() != 0 || f.fixture.workerCalls != 0 || f.fixture.diskCalls != 0 {
			t.Errorf("capacity readiness failure admitted later checks: strict=%d worker=%d disk=%d", f.strictCount(), f.fixture.workerCalls, f.fixture.diskCalls)
		}
		assertReadinessRejected(t, result, PreflightCodeTargetUnavailable)
	})
}

func TestPreflightReadinessRecoversAfterEarlierScriptSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newReadinessFixture(func(_ context.Context, node int, path string, attempt int) (int, error) {
			if node == 0 && path == "/readyz" && attempt == 2 {
				return http.StatusServiceUnavailable, nil
			}
			return http.StatusOK, nil
		})
		// The script's prior successful request cannot guarantee the later cut.
		if err := f.targets[0].Readyz(context.Background()); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		result := f.fixture.preflight.Check(context.Background(), f.fixture.cfg)
		if !result.Passed() {
			t.Fatalf("one transient readyz failure after script success terminated preflight: %+v", result)
		}
		if elapsed := time.Since(started); elapsed != 250*time.Millisecond {
			t.Fatalf("retry interval=%s, want 250ms", elapsed)
		}
		if got := f.probeCount(0, "/readyz"); got != 3 {
			t.Fatalf("script plus coordinator readiness probes=%d, want 3", got)
		}
	})
}

func TestPreflightReadinessRecoversHealthTransport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newReadinessFixture(func(_ context.Context, node int, path string, attempt int) (int, error) {
			if node == 0 && path == "/healthz" && attempt == 1 {
				return 0, errors.New("connection reset CANARY_transport")
			}
			return http.StatusOK, nil
		})
		if result := f.fixture.preflight.Check(context.Background(), f.fixture.cfg); !result.Passed() {
			t.Fatalf("recoverable health transport result=%+v", result)
		}
		if got := f.probeCount(0, "/healthz"); got != 2 {
			t.Fatalf("health attempts=%d, want 2", got)
		}
	})
}

func TestPreflightReadinessGatesEveryNodeBeforeStrictChecks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var f *readinessFixture
		f = newReadinessFixture(func(_ context.Context, node int, path string, attempt int) (int, error) {
			if node == 2 && path == "/readyz" && attempt == 1 {
				if got := f.strictCount(); got != 0 {
					t.Errorf("strict target work started before all service readiness: %d calls", got)
				}
				return http.StatusServiceUnavailable, nil
			}
			return http.StatusOK, nil
		})
		if result := f.fixture.preflight.Check(context.Background(), f.fixture.cfg); !result.Passed() {
			t.Fatalf("last service recovery result=%+v", result)
		}
	})
}

func TestPreflightReadinessPersistentFailureHasSharedThirtySecondBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newReadinessFixture(func(_ context.Context, _ int, path string, _ int) (int, error) {
			if path == "/readyz" {
				return http.StatusServiceUnavailable, nil
			}
			return http.StatusOK, nil
		})
		started := time.Now()
		result := f.fixture.preflight.Check(context.Background(), f.fixture.cfg)
		assertReadinessRejected(t, result, PreflightCodeTargetUnavailable)
		if elapsed := time.Since(started); elapsed != 30*time.Second {
			t.Fatalf("shared startup wait=%s, want 30s", elapsed)
		}
		if calls := f.probeCount(0, "/readyz"); calls < 120 || calls > 121 {
			t.Fatalf("ready attempts=%d, want bounded 250ms rounds within 30s", calls)
		}
		if f.strictCount() != 0 || f.fixture.workerCalls != 0 || f.fixture.diskCalls != 0 {
			t.Fatal("failed readiness admitted strict target, worker, or disk checks")
		}
	})
}

func TestPreflightReadinessBlockedProbeUsesSharedDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newReadinessFixture(func(ctx context.Context, _ int, _ string, _ int) (int, error) {
			<-ctx.Done()
			return 0, ctx.Err()
		})
		started := time.Now()
		result := f.fixture.preflight.Check(context.Background(), f.fixture.cfg)
		assertReadinessRejected(t, result, PreflightCodeTargetUnavailable)
		if elapsed := time.Since(started); elapsed != 30*time.Second {
			t.Fatalf("blocked probe elapsed=%s, want startup context to bound it at 30s", elapsed)
		}
		if f.totalProbes() != 1 || f.strictCount() != 0 {
			t.Fatal("exhausted probe deadline allowed later work")
		}
	})
}

func TestPreflightReadinessBudgetDoesNotRestartForAnotherService(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		f := newReadinessFixture(func(_ context.Context, node int, path string, _ int) (int, error) {
			if path == "/readyz" && (node == 1 || node == 0 && time.Since(started) < 15*time.Second) {
				return http.StatusServiceUnavailable, nil
			}
			return http.StatusOK, nil
		})
		result := f.fixture.preflight.Check(context.Background(), f.fixture.cfg)
		assertReadinessRejected(t, result, PreflightCodeTargetUnavailable)
		if elapsed := time.Since(started); elapsed != 30*time.Second {
			t.Fatalf("startup budget restarted when another service failed: elapsed=%s, want 30s total", elapsed)
		}
		if f.probeCount(1, "/readyz") == 0 || f.strictCount() != 0 {
			t.Fatal("later unhealthy service was not checked inside the same admission gate")
		}
	})
}

func TestPreflightReadinessHonorsParentCancellationAndDeadline(t *testing.T) {
	for _, mode := range []string{"already_canceled", "cancel_after_failure", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				wantElapsed := time.Duration(0)
				if mode == "already_canceled" {
					cancel()
				} else if mode == "deadline" {
					var deadlineCancel context.CancelFunc
					ctx, deadlineCancel = context.WithTimeout(ctx, 750*time.Millisecond)
					defer deadlineCancel()
					wantElapsed = 750 * time.Millisecond
				}
				f := newReadinessFixture(func(_ context.Context, _ int, _ string, _ int) (int, error) {
					if mode == "cancel_after_failure" {
						cancel()
					}
					return http.StatusServiceUnavailable, nil
				})
				started := time.Now()
				result := f.fixture.preflight.Check(ctx, f.fixture.cfg)
				assertReadinessRejected(t, result, PreflightCodeTargetUnavailable)
				if elapsed := time.Since(started); elapsed != wantElapsed {
					t.Fatalf("parent %s elapsed=%s, want %s", mode, elapsed, wantElapsed)
				}
				if f.strictCount() != 0 || f.totalProbes() > 4 || mode == "already_canceled" && f.totalProbes() != 0 {
					t.Fatalf("parent cancellation did not stop probes: calls=%d strict=%d", f.totalProbes(), f.strictCount())
				}
			})
		})
	}
}

func TestPreflightReadinessAuthorizationFailsImmediately(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, path := range []string{"/healthz", "/readyz"} {
			t.Run(fmt.Sprintf("%d%s", code, path), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					f := newReadinessFixture(func(_ context.Context, _ int, requested string, _ int) (int, error) {
						if requested == path {
							return code, nil
						}
						return http.StatusOK, nil
					})
					started := time.Now()
					result := f.fixture.preflight.Check(context.Background(), f.fixture.cfg)
					assertReadinessRejected(t, result, PreflightCodeUnauthorized)
					if time.Since(started) != 0 || f.probeCount(0, path) != 1 || f.strictCount() != 0 {
						t.Fatal("authorization failure was delayed, retried, or admitted later work")
					}
				})
			})
		}
	}
}

func TestPreflightReadinessDoesNotRetryStrictFailures(t *testing.T) {
	for _, stage := range []string{"config", "metrics", "cluster"} {
		t.Run(stage, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := newReadinessFixture(nil)
				wantCode := PreflightCodeTargetConfig
				switch stage {
				case "config":
					f.targets[0].config.MaxChannels++
				case "metrics":
					f.targets[0].metricsErr = errors.New("CANARY_metrics")
					wantCode = PreflightCodeMetrics
				case "cluster":
					f.targets[0].clusterErr = errors.New("CANARY_cluster")
					wantCode = PreflightCodeCluster
				}
				started := time.Now()
				result := f.fixture.preflight.Check(context.Background(), f.fixture.cfg)
				assertReadinessRejected(t, result, wantCode)
				if time.Since(started) != 0 || f.strictCalls[stage] != 1 {
					t.Fatalf("strict %s failure was retried: %v", stage, f.strictCalls)
				}
				if f.totalProbes() != 6 {
					t.Fatalf("strict failure should follow one complete three-service gate, probes=%d", f.totalProbes())
				}
			})
		})
	}
}

func TestPreflightReadinessDiagnosticsAreBoundedAndRedacted(t *testing.T) {
	for _, scenario := range []string{"ready_recovery", "transport_recovery", "unauthorized", "canceled", "exhausted"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				check, final := "readyz", "ready"
				if scenario == "transport_recovery" {
					check = "healthz"
				}
				if scenario == "unauthorized" || scenario == "canceled" || scenario == "exhausted" {
					final = scenario
				}
				f := newReadinessFixture(func(_ context.Context, node int, path string, attempt int) (int, error) {
					if node != 0 || path != "/"+check {
						return http.StatusOK, nil
					}
					switch scenario {
					case "ready_recovery":
						if attempt == 1 {
							return http.StatusServiceUnavailable, nil
						}
					case "transport_recovery":
						if attempt == 1 {
							return 0, errors.New("CANARY_transport_error")
						}
					case "unauthorized":
						return http.StatusForbidden, nil
					case "canceled":
						cancel()
						return 0, context.Canceled
					case "exhausted":
						return http.StatusServiceUnavailable, nil
					}
					return http.StatusOK, nil
				})
				var log bytes.Buffer
				f.fixture.preflight.options.DiagnosticLog = &log
				result := f.fixture.preflight.Check(ctx, f.fixture.cfg)
				if final == "ready" && !result.Passed() || final != "ready" && result.TrafficAllowed() {
					t.Fatalf("diagnostic scenario outcome=%+v", result)
				}
				body := log.String()
				for _, required := range []string{"[chat-lifecycle-preflight]", "service_index=1", "check=" + check, "attempt=1", "result=" + final} {
					if !strings.Contains(body, required) {
						t.Errorf("missing diagnostic %q in %q", required, body)
					}
				}
				if final == "ready" || final == "exhausted" {
					if !strings.Contains(body, "result=retry") {
						t.Errorf("missing retry diagnostic in %q", body)
					}
				}
				for _, forbidden := range []string{"CANARY", "http://", "Bearer", "connection reset", "context canceled"} {
					if strings.Contains(body, forbidden) {
						t.Errorf("diagnostic leaked %q: %q", forbidden, body)
					}
				}
				if len(body) > 32*1024 || strings.Count(body, "\n") > 124 {
					t.Fatalf("startup diagnostics exceeded bounded retry history: bytes=%d lines=%d", len(body), strings.Count(body, "\n"))
				}
			})
		})
	}
}

type readinessFixture struct {
	fixture     *preflightFixture
	targets     []*readinessTarget
	mu          sync.Mutex
	probes      map[string]int
	strictCalls map[string]int
}

type readinessTarget struct {
	*fakePreflightTarget
	client     *target.Client
	owner      *readinessFixture
	metricsErr error
}

func newReadinessFixture(respond func(context.Context, int, string, int) (int, error)) *readinessFixture {
	return newReadinessFixtureForConfig(LocalConfig(), respond)
}

func newReadinessFixtureForConfig(cfg Config, respond func(context.Context, int, string, int) (int, error)) *readinessFixture {
	f := &readinessFixture{fixture: newPreflightFixture(cfg), probes: make(map[string]int), strictCalls: make(map[string]int)}
	for index, fake := range f.fixture.targets {
		client := target.NewClient(target.Config{
			APIAddrs: []string{fmt.Sprintf("http://CANARY_endpoint_%d.invalid", index)}, Token: "CANARY_bearer_token",
			HTTPClient: &http.Client{Timeout: 60 * time.Second, Transport: workerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				f.mu.Lock()
				key := fmt.Sprintf("%d%s", index, request.URL.Path)
				f.probes[key]++
				attempt := f.probes[key]
				f.mu.Unlock()
				status, err := http.StatusOK, error(nil)
				if respond != nil {
					status, err = respond(request.Context(), index, request.URL.Path, attempt)
				}
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("CANARY_response_body")), Request: request}, nil
			})},
		})
		f.targets = append(f.targets, &readinessTarget{fakePreflightTarget: fake, client: client, owner: f})
	}
	f.fixture.preflight.options.TargetFactory = func(index int, _ EndpointDeclaration, _ string) PreflightTarget { return f.targets[index] }
	return f
}

func (f *readinessFixture) probeCount(node int, path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.probes[fmt.Sprintf("%d%s", node, path)]
}

func (f *readinessFixture) totalProbes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, value := range f.probes {
		count += value
	}
	return count
}

func (f *readinessFixture) strictCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, value := range f.strictCalls {
		count += value
	}
	return count
}

func (f *readinessFixture) recordStrict(stage string) {
	f.mu.Lock()
	f.strictCalls[stage]++
	f.mu.Unlock()
}

func (s *readinessTarget) Healthz(ctx context.Context) error { return s.client.Healthz(ctx) }
func (s *readinessTarget) Readyz(ctx context.Context) error  { return s.client.Readyz(ctx) }
func (s *readinessTarget) DebugConfig(ctx context.Context) (target.DebugConfig, error) {
	s.owner.recordStrict("config")
	return s.fakePreflightTarget.DebugConfig(ctx)
}
func (s *readinessTarget) DebugCluster(ctx context.Context) (target.DebugCluster, error) {
	s.owner.recordStrict("cluster")
	return s.fakePreflightTarget.DebugCluster(ctx)
}
func (s *readinessTarget) ForceGC(ctx context.Context) error {
	s.owner.recordStrict("gc")
	return s.fakePreflightTarget.ForceGC(ctx)
}
func (s *readinessTarget) CheckMetrics(ctx context.Context) error {
	s.owner.recordStrict("metrics")
	if s.metricsErr != nil {
		return s.metricsErr
	}
	return s.fakePreflightTarget.CheckMetrics(ctx)
}
func (s *readinessTarget) Capabilities(ctx context.Context) (model.BenchCapabilities, error) {
	s.owner.recordStrict("capabilities")
	return s.fakePreflightTarget.Capabilities(ctx)
}

func assertReadinessRejected(t *testing.T, result PreflightResult, code PreflightCode) {
	t.Helper()
	if result.Outcome != PreflightHarnessInvalid || result.Code != code || result.TrafficAllowed() {
		t.Fatalf("preflight result=%+v, want harness_invalid/%s without traffic", result, code)
	}
}
