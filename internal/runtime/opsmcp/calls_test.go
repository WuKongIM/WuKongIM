package opsmcp

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCallControlSeparatesLogRateAndAuditsBoundedSelectors(t *testing.T) {
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	control := NewCallControl(CallControlConfig{Now: func() time.Time { return now }})
	metadata := CallMetadata{
		RequestID: "request", Principal: Principal{CredentialID: "credential"},
		Tool: "logs_search", NodeID: 3,
	}
	for index := 0; index < logCallsPerMinute; index++ {
		_, finish, err := control.BeginCall(context.Background(), metadata)
		if err != nil {
			t.Fatalf("BeginCall(%d) error = %v", index, err)
		}
		finish(CallFinish{Result: "ok", ResponseBytes: 42})
	}
	if _, _, err := control.BeginCall(context.Background(), metadata); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate error = %v", err)
	}
	audits, err := control.RecentAudits(context.Background(), 1)
	if err != nil || len(audits) != 1 {
		t.Fatalf("RecentAudits()=%#v error=%v", audits, err)
	}
	if audits[0].CredentialID != "credential" || audits[0].Tool != "logs_search" ||
		audits[0].Target.NodeID != 3 || audits[0].Result != "rate_limited" {
		t.Fatalf("audit = %#v", audits[0])
	}
}

func TestCallControlLimitsFailedAuthenticationByTCPSource(t *testing.T) {
	observer := &fakeCallObserver{}
	control := NewCallControl(CallControlConfig{Observer: observer})
	for index := 0; index < authFailuresPerSourceMinute; index++ {
		if !control.AllowAuthentication("10.0.0.1:1234") {
			t.Fatalf("source blocked before failure %d", index)
		}
		control.RecordAuthFailure("10.0.0.1:1234", "request")
	}
	if control.AllowAuthentication("10.0.0.1:9999") {
		t.Fatal("same TCP source was not limited")
	}
	if !control.AllowAuthentication("10.0.0.2:1234") {
		t.Fatal("unrelated TCP source was limited")
	}
	if observer.authFailures != authFailuresPerSourceMinute {
		t.Fatalf("auth failure observations = %d, want %d", observer.authFailures, authFailuresPerSourceMinute)
	}
}

func TestCallControlObservesAdmittedAndRejectedCalls(t *testing.T) {
	observer := &fakeCallObserver{}
	control := NewCallControl(CallControlConfig{Observer: observer})
	metadata := CallMetadata{
		Principal: Principal{CredentialID: "credential"}, Tool: "cluster_health",
	}
	for index := 0; index < ordinaryCallsPerMinute; index++ {
		_, finish, err := control.BeginCall(context.Background(), metadata)
		if err != nil {
			t.Fatalf("BeginCall(%d) error = %v", index, err)
		}
		finish(CallFinish{Result: "ok"})
	}
	if _, _, err := control.BeginCall(context.Background(), metadata); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate error = %v", err)
	}
	if observer.started != ordinaryCallsPerMinute || observer.finished != ordinaryCallsPerMinute ||
		observer.rejected != 1 || observer.lastResult != "rate_limited" {
		t.Fatalf("observer = %#v", observer)
	}
}

func TestCallControlPrunesInactiveCredentialBudgets(t *testing.T) {
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	control := NewCallControl(CallControlConfig{Now: func() time.Time { return now }})
	for index := 0; index < 20; index++ {
		_, finish, err := control.BeginCall(context.Background(), CallMetadata{
			Principal: Principal{CredentialID: fmt.Sprintf("credential-%d", index)},
			Tool:      "cluster_health",
		})
		if err != nil {
			t.Fatalf("BeginCall(%d) error = %v", index, err)
		}
		finish(CallFinish{Result: "ok"})
	}
	now = now.Add(time.Minute)
	_, finish, err := control.BeginCall(context.Background(), CallMetadata{
		Principal: Principal{CredentialID: "current"}, Tool: "cluster_health",
	})
	if err != nil {
		t.Fatalf("BeginCall(current) error = %v", err)
	}
	finish(CallFinish{Result: "ok"})
	if len(control.credential) != 1 {
		t.Fatalf("credential budgets = %d, want only current window", len(control.credential))
	}
}

func TestCallControlAuditsRemoteIngressWithoutArguments(t *testing.T) {
	control := NewCallControl(CallControlConfig{})
	finish, err := control.BeginIngress(
		Principal{CredentialID: "credential-a"}, "request-a", 2, 1,
	)
	if err != nil {
		t.Fatalf("BeginIngress() error = %v", err)
	}
	finish("forwarded")
	audits, err := control.RecentAudits(context.Background(), 1)
	if err != nil || len(audits) != 1 {
		t.Fatalf("RecentAudits()=%#v error=%v", audits, err)
	}
	got := audits[0]
	if got.Phase != "ingress" || got.IngressNodeID != 2 || got.OwnerNodeID != 1 ||
		got.CredentialID != "credential-a" || got.Tool != "" || got.Result != "forwarded" {
		t.Fatalf("ingress audit = %#v", got)
	}
}

type fakeCallObserver struct {
	started      int
	finished     int
	rejected     int
	authFailures int
	lastResult   string
}

func (f *fakeCallObserver) CallStarted(string) {
	f.started++
}

func (f *fakeCallObserver) CallFinished(_ string, result string, _ time.Duration) {
	f.finished++
	f.lastResult = result
}

func (f *fakeCallObserver) CallRejected(_ string, result string) {
	f.rejected++
	f.lastResult = result
}

func (f *fakeCallObserver) AuthenticationFailed() {
	f.authFailures++
}
