package opsmcp

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
)

func TestForwardVerifierFailsClosedForUnavailableAndMalformedCredentials(t *testing.T) {
	ctx := context.Background()
	digest := strings.Repeat("ab", 32)
	validState := DesiredState{
		Revision: 7, Enabled: true, OwnerNodeID: 2,
		Credentials: []Credential{{ID: "credential", DigestSHA256: digest}},
	}
	tests := []struct {
		name     string
		verifier *Verifier
		id       string
		digest   string
		revision uint64
		want     error
	}{
		{name: "nil verifier", verifier: nil, id: "credential", digest: digest, revision: 7, want: ErrOwnerUnavailable},
		{name: "missing state", verifier: &Verifier{}, id: "credential", digest: digest, revision: 7, want: ErrOwnerUnavailable},
		{name: "state read", verifier: NewNodeVerifier(stateReaderStub{err: errors.New("read failed")}, 2), id: "credential", digest: digest, revision: 7, want: ErrOwnerUnavailable},
		{name: "disabled", verifier: NewNodeVerifier(stateReaderStub{state: DesiredState{}}, 2), id: "credential", digest: digest, revision: 7, want: ErrDisabled},
		{name: "malformed digest", verifier: NewNodeVerifier(stateReaderStub{state: validState}, 2), id: "credential", digest: "not-hex", revision: 7, want: ErrUnauthorized},
		{name: "invalid id", verifier: NewNodeVerifier(stateReaderStub{state: validState}, 2), id: "Credential", digest: digest, revision: 7, want: ErrUnauthorized},
		{name: "missing id", verifier: NewNodeVerifier(stateReaderStub{state: validState}, 2), id: "other", digest: digest, revision: 7, want: ErrUnauthorized},
		{name: "bad stored digest", verifier: NewNodeVerifier(stateReaderStub{state: DesiredState{Revision: 7, Enabled: true, OwnerNodeID: 2, Credentials: []Credential{{ID: "credential", DigestSHA256: "bad"}}}}, 2), id: "credential", digest: digest, revision: 7, want: ErrUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.verifier.VerifyForward(ctx, tt.id, tt.digest, tt.revision); !errors.Is(err, tt.want) {
				t.Fatalf("VerifyForward() error = %v, want %v", err, tt.want)
			}
		})
	}
	if !IsAuthenticationFailure(ErrUnauthorized) || IsAuthenticationFailure(ErrDisabled) || !IsAuthenticationFailure(errors.Join(errors.New("wrapped"), ErrUnauthorized)) {
		t.Fatal("authentication failure classification is not errors.Is compatible")
	}
}

func TestCredentialIDsRemainBoundedLowercaseIdentifiers(t *testing.T) {
	for _, valid := range []string{"a", "credential_1", "ops-read-only", strings.Repeat("a", 64)} {
		if !validCredentialID(valid) {
			t.Fatalf("valid credential ID %q rejected", valid)
		}
	}
	for _, invalid := range []string{"", "Upper", "contains.dot", "contains space", strings.Repeat("a", 65)} {
		if validCredentialID(invalid) {
			t.Fatalf("invalid credential ID %q accepted", invalid)
		}
	}
}

func TestCallControlIngressBudgetsAndCompletionAreBounded(t *testing.T) {
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	principal := Principal{CredentialID: "credential"}
	control := NewCallControl(CallControlConfig{Now: func() time.Time { return now }})
	for index := 0; index < ingressRequestsPerMinute; index++ {
		finish, err := control.BeginIngress(principal, "request", 2, 1)
		if err != nil {
			t.Fatalf("BeginIngress(%d): %v", index, err)
		}
		finish("forwarded")
		finish("must-be-idempotent")
	}
	if _, err := control.BeginIngress(principal, "limited", 2, 1); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("ingress rate error = %v", err)
	}

	concurrent := NewCallControl(CallControlConfig{Now: func() time.Time { return now }})
	finishes := make([]func(string), 0, concurrentCallsPerCredential)
	for index := 0; index < concurrentCallsPerCredential; index++ {
		finish, err := concurrent.BeginIngress(principal, "active", 2, 1)
		if err != nil {
			t.Fatalf("concurrent ingress %d: %v", index, err)
		}
		finishes = append(finishes, finish)
	}
	if _, err := concurrent.BeginIngress(principal, "too-many", 2, 1); !errors.Is(err, ErrConcurrencyLimited) {
		t.Fatalf("ingress concurrency error = %v", err)
	}
	for _, finish := range finishes {
		finish("forwarded")
	}

	for _, input := range []struct {
		principal Principal
		requestID string
		ingress   uint64
		owner     uint64
	}{
		{requestID: "request", ingress: 2, owner: 1},
		{principal: principal, ingress: 2, owner: 1},
		{principal: principal, requestID: "request", owner: 1},
		{principal: principal, requestID: "request", ingress: 2},
	} {
		if _, err := control.BeginIngress(input.principal, input.requestID, input.ingress, input.owner); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("invalid ingress %+v error = %v", input, err)
		}
	}
}

func TestCallControlCloseAuditQueriesAndTCPSourceAreSafe(t *testing.T) {
	if err := (*CallControl)(nil).Close(); err != nil {
		t.Fatalf("nil Close() = %v", err)
	}
	control := NewCallControl(CallControlConfig{})
	if err := control.Close(); err != nil {
		t.Fatalf("empty Close() = %v", err)
	}
	writer := &closeRecorder{err: errors.New("close failed")}
	control.writer = writer
	if err := control.Close(); !errors.Is(err, writer.err) || writer.calls != 1 || control.writer != nil {
		t.Fatalf("writer close = %v calls=%d writer=%v", err, writer.calls, control.writer)
	}
	if err := control.Close(); err != nil || writer.calls != 1 {
		t.Fatalf("second Close() = %v calls=%d", err, writer.calls)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := control.RecentAudits(canceled, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled audit query error = %v", err)
	}
	if _, err := (*CallControl)(nil).RecentAudits(context.Background(), 1); err == nil {
		t.Fatal("nil audit controller unexpectedly accepted")
	}
	for _, limit := range []int{0, maxRecentAudits + 1} {
		if _, err := control.RecentAudits(context.Background(), limit); err == nil {
			t.Fatalf("invalid audit limit %d accepted", limit)
		}
	}

	for input, want := range map[string]string{
		"10.0.0.1:1234": "10.0.0.1",
		"[::1]:9090":    "::1",
		"bare-host":     "bare-host",
		"  ":            "unknown",
	} {
		if got := tcpSource(input); got != want {
			t.Fatalf("tcpSource(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestProfileRequestWriterAndSampleProjectionBoundaries(t *testing.T) {
	valid := []opscontract.ProfileRequest{
		{Kind: "cpu", Seconds: 1}, {Kind: "cpu", Seconds: 30},
		{Kind: "heap"}, {Kind: "goroutine"},
	}
	for _, request := range valid {
		if !validProfileRequest(request) {
			t.Fatalf("valid profile request rejected: %+v", request)
		}
	}
	invalid := []opscontract.ProfileRequest{
		{Kind: "cpu"}, {Kind: "cpu", Seconds: 31}, {Kind: "heap", Seconds: 1},
		{Kind: "goroutine", Seconds: -1}, {Kind: "thread"},
	}
	for _, request := range invalid {
		if validProfileRequest(request) {
			t.Fatalf("invalid profile request accepted: %+v", request)
		}
	}

	writer := &profileLimitWriter{remaining: 4}
	if count, err := writer.Write([]byte("four")); err != nil || count != 4 || writer.remaining != 0 || writer.buffer.String() != "four" {
		t.Fatalf("exact profile write = %d, %v, remaining=%d body=%q", count, err, writer.remaining, writer.buffer.String())
	}
	if count, err := writer.Write([]byte("x")); !errors.Is(err, ErrProfileTooLarge) || count != 0 || !writer.exceeded {
		t.Fatalf("oversize profile write = %d, %v exceeded=%t", count, err, writer.exceeded)
	}
	if _, err := captureRuntimeProfile(context.Background(), opscontract.ProfileRequest{Kind: "unsupported"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unsupported profile capture error = %v", err)
	}
	if got := sampleTypeOf(nil); got != "" {
		t.Fatalf("empty sample type = %q", got)
	}
	if got := sampleTypeOf([]ProfileRow{{SampleType: "inuse_space"}}); got != "inuse_space" {
		t.Fatalf("sample type = %q", got)
	}
}

func TestAuditEntryDefaultsCPUSecondsWithoutRetainingArguments(t *testing.T) {
	entry := auditEntry(CallMetadata{
		RequestID: "request", Principal: Principal{CredentialID: "credential", OwnerNodeID: 3},
		Tool: "pprof_analyze", NodeID: 2, SlotID: 7, ChannelType: 1, PprofKind: "cpu",
	}, time.Unix(100, 0), "ok", -5, 42, true)
	if entry.PprofSeconds != 10 || entry.Target.NodeID != 2 || entry.Target.SlotID != 7 || entry.Target.ChannelType != 1 ||
		entry.DurationMS != -5 || entry.ResponseBytes != 42 || !entry.CacheHit || entry.CredentialID != "credential" {
		t.Fatalf("audit entry = %+v", entry)
	}
}

type closeRecorder struct {
	calls int
	err   error
}

func (*closeRecorder) Write(payload []byte) (int, error) { return len(payload), nil }

func (w *closeRecorder) Close() error {
	w.calls++
	return w.err
}

var _ io.WriteCloser = (*closeRecorder)(nil)
