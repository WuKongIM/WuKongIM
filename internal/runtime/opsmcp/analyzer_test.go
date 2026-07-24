package opsmcp

import (
	"context"
	"errors"
	"testing"
	"time"

	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
)

func TestProfileAnalyzerReturnsRowsAndDoesNotRetainRawProfile(t *testing.T) {
	client := &profileClientStub{}
	analyzer := NewProfileAnalyzer(stateReaderStub{state: DesiredState{
		Revision: 12, Enabled: true, OwnerNodeID: 1,
	}}, client, 1)
	times := []time.Time{
		time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 24, 1, 0, 1, 0, time.UTC),
	}
	analyzer.now = func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}
	data, window, err := analyzer.AnalyzeOpsProfile(context.Background(), ProfileAnalysisRequest{
		NodeID: 3, Kind: "goroutine", Seconds: 10, Rows: 30,
	})
	if err != nil {
		t.Fatalf("AnalyzeOpsProfile() error = %v", err)
	}
	result := data.(profileAnalysis)
	if result.NodeID != 3 || result.Kind != "goroutine" || len(result.Rows) == 0 {
		t.Fatalf("analysis = %#v", result)
	}
	if !window.Start.Equal(time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)) || client.request.ExpectedRevision != 12 ||
		client.request.OwnerNodeID != 1 {
		t.Fatalf("window=%#v request=%#v", window, client.request)
	}
}

func TestProfileAnalyzerHonorsOwnerTransitionFence(t *testing.T) {
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	client := &profileClientStub{}
	analyzer := NewProfileAnalyzer(stateReaderStub{state: DesiredState{
		Revision: 12, Enabled: true, OwnerNodeID: 1,
		ProfileFenceUntilUnixMillis: now.Add(time.Second).UnixMilli(),
	}}, client, 1)
	analyzer.now = func() time.Time { return now }

	_, _, err := analyzer.AnalyzeOpsProfile(context.Background(), ProfileAnalysisRequest{
		NodeID: 3, Kind: "goroutine", Rows: 30,
	})
	if err != ErrProfileCooldown || client.request.NodeID != 0 {
		t.Fatalf("AnalyzeOpsProfile() error = %v request=%#v, want fenced before RPC", err, client.request)
	}
}

func TestProfileAnalyzerIssuesAndConsumesOneTimeLease(t *testing.T) {
	state := stateReaderStub{state: DesiredState{Revision: 12, Enabled: true, OwnerNodeID: 1}}
	client := &profileLeaseClientStub{}
	analyzer := NewProfileAnalyzer(state, client, 1)
	client.analyzer = analyzer
	if _, _, err := analyzer.AnalyzeOpsProfile(context.Background(), ProfileAnalysisRequest{
		NodeID: 3, Kind: "goroutine", Rows: 30,
	}); err != nil {
		t.Fatalf("AnalyzeOpsProfile() error = %v", err)
	}
	if client.request.LeaseID == "" {
		t.Fatal("profile request omitted lease ID")
	}
	if err := analyzer.AuthorizeProfileLease(context.Background(), opscontract.ProfileLeaseRequest{
		Version: opscontract.RPCVersion, OwnerNodeID: 1, ExpectedRevision: 12,
		TargetNodeID: 3, LeaseID: client.request.LeaseID,
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("reused lease error = %v, want unauthorized", err)
	}
}

type profileClientStub struct {
	request opscontract.ProfileRequest
}

func (c *profileClientStub) CaptureOpsMCPProfile(ctx context.Context, _ uint64, request opscontract.ProfileRequest) (opscontract.ProfileResponse, error) {
	c.request = request
	payload, err := captureRuntimeProfile(ctx, opscontract.ProfileRequest{Kind: "goroutine"})
	if err != nil {
		return opscontract.ProfileResponse{}, err
	}
	return opscontract.ProfileResponse{Version: opscontract.RPCVersion, Payload: payload}, nil
}

type profileLeaseClientStub struct {
	analyzer *ProfileAnalyzer
	request  opscontract.ProfileRequest
}

func (c *profileLeaseClientStub) CaptureOpsMCPProfile(ctx context.Context, _ uint64, request opscontract.ProfileRequest) (opscontract.ProfileResponse, error) {
	c.request = request
	if err := c.analyzer.AuthorizeProfileLease(ctx, opscontract.ProfileLeaseRequest{
		Version: request.Version, OwnerNodeID: request.OwnerNodeID, ExpectedRevision: request.ExpectedRevision,
		TargetNodeID: request.NodeID, LeaseID: request.LeaseID,
	}); err != nil {
		return opscontract.ProfileResponse{}, err
	}
	payload, err := captureRuntimeProfile(ctx, opscontract.ProfileRequest{Kind: "goroutine"})
	if err != nil {
		return opscontract.ProfileResponse{}, err
	}
	return opscontract.ProfileResponse{Version: opscontract.RPCVersion, Payload: payload}, nil
}
