package node

import (
	"context"
	"errors"
	"reflect"
	"testing"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func wireGeneration() authority.GenerationClaim {
	return authority.GenerationClaim{Target: authority.RouteTarget{HashSlot: 37, SlotID: 3, LeaderNodeID: 9, LeaderTerm: 4, ConfigEpoch: 2}, OwnerSetEpoch: 11, ServingBoot: 19, Generation: 100, PreviousGeneration: 99, AppliedRaftIndex: 100}
}
func wireCut() authority.OwnerCut { return authority.OwnerCut{NodeID: 7, BootID: 17, Cut: 3} }
func wireRoute() presence.Route {
	return presence.Route{UID: "user", OwnerNodeID: 7, OwnerBootID: 17, OwnerSeq: 8, SessionID: 88, DeviceID: "d", DeviceFlag: 1, DeviceLevel: 1, Listener: "tcp", ConnectedUnix: 10, LastSeenUnix: 20}
}

func TestGenerationWireRoundTripUsesIndependentVersion(t *testing.T) {
	for _, op := range []byte{generationRegister, generationCommit, generationAbortRoute, generationBegin, generationPage, generationEnd, generationAbortSlot, generationReadUIDs, generationTouch, generationUnregister} {
		request := generationRequest{Op: op, Claim: wireGeneration(), Owner: wireCut(), Sequence: 2, AfterSessionID: 77, RowsTotal: 3, MaxRows: 100, MaxBytes: 16384, Route: wireRoute(), Token: "opaque", Routes: []presence.Route{wireRoute()}, Identity: wireRoute().Identity(), OwnerSeq: 8, UIDs: []string{"a", "b"}}
		body, err := encodeGenerationRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeGenerationRequest(body)
		if err != nil || !reflect.DeepEqual(request, got) {
			t.Fatalf("op %d: %+v %v", op, got, err)
		}
		if _, err = decodePresenceRPCRequest(body); err == nil {
			t.Fatal("new message interpreted as legacy")
		}
		for _, bad := range [][]byte{body[:len(body)-1], append(append([]byte(nil), body...), 0)} {
			if _, err = decodeGenerationRequest(bad); err == nil {
				t.Fatal("truncated/trailing payload accepted")
			}
		}
		wrong := append([]byte(nil), body...)
		wrong[4]++
		if _, err = decodeGenerationRequest(wrong); err == nil {
			t.Fatal("unknown version accepted")
		}
	}
	if _, err := encodeGenerationRequest(generationRequest{Op: 255}); err == nil {
		t.Fatal("unknown operation encoded")
	}
}

func TestGenerationPageDecodeRequiresExactCutAndPrepaidBacking(t *testing.T) {
	claim, cut := wireGeneration(), wireCut()
	req := authority.SlotPageRequest{Claim: claim, Owner: cut, Sequence: 2, AfterSessionID: 77, MaxRows: 100, MaxBytes: 16384}
	resp := generationResponse{Op: generationPage, Status: rpcStatusOK, Claim: claim, Owner: cut, Sequence: 2, AfterSessionID: 77, NextSessionID: 88, RowsTotal: 3, Exhausted: true, Routes: []presence.Route{wireRoute()}}
	body, err := encodeGenerationResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	budget := presence.NewRecoveryResultBudget(4096, 1<<20)
	page, err := decodeGenerationPage(context.Background(), body, req, budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Page.Routes) != 1 || !reflect.DeepEqual(page.Page.Routes[0], wireRoute()) {
		t.Fatal("route fields changed")
	}
	if stats := budget.Stats(); stats.Rows != 1 || stats.Bytes == 0 {
		t.Fatalf("unaccounted page: %+v", stats)
	}
	page.Release()
	page.Release()
	if stats := budget.Stats(); stats.Rows != 0 || stats.Bytes != 0 {
		t.Fatalf("page leak: %+v", stats)
	}
	wrong := req
	wrong.Owner.BootID++
	if _, err = decodeGenerationPage(context.Background(), body, wrong, budget); err == nil {
		t.Fatal("foreign boot accepted")
	}
	tiny := presence.NewRecoveryResultBudget(0, 1<<20)
	if _, err = decodeGenerationPage(context.Background(), body, req, tiny); !errors.Is(err, presence.ErrRecoveryResultBudget) {
		t.Fatalf("capacity rejection: %v", err)
	}
	if stats := tiny.Stats(); stats.Rows != 0 || stats.Bytes != 0 {
		t.Fatalf("partial credit retained: %+v", stats)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = decodeGenerationPage(ctx, body, req, budget); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled decode: %v", err)
	}
	for _, change := range []func(*generationResponse){
		func(x *generationResponse) { x.Sequence++ }, func(x *generationResponse) { x.Claim.Generation++ },
		func(x *generationResponse) { x.Status = "unknown" }, func(x *generationResponse) { x.Op = generationEnd },
		func(x *generationResponse) { x.NextSessionID = 76 }, func(x *generationResponse) { x.RowsTotal = 0 },
	} {
		invalid := resp
		change(&invalid)
		data, err := encodeGenerationResponse(invalid)
		if err == nil {
			if _, err = decodeGenerationPage(context.Background(), data, req, budget); err == nil {
				t.Fatal("invalid page accepted")
			}
		}
	}
}

func TestGenerationWireNeverSilentlyDropsActivationProof(t *testing.T) {
	claim, cut, route := wireGeneration(), wireCut(), wireRoute()
	receipt := authority.ActivationReceipt{Claim: claim, Owner: cut, Identity: route.Identity(), OwnerSeq: route.OwnerSeq}
	pending := claim
	for _, result := range []presence.RegisterResult{{Receipt: &receipt}, {PendingToken: "token", PendingClaim: &pending, Actions: []presence.RouteAction{{UID: "u", OwnerNodeID: 7, OwnerBootID: 17, SessionID: 8, Kind: "close", Reason: "conflict"}}}} {
		response := generationResponse{Op: generationRegister, Status: rpcStatusOK, Register: result}
		data, err := encodeGenerationResponse(response)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeGenerationResponse(data)
		if err != nil || !reflect.DeepEqual(result, got.Register) {
			t.Fatalf("activation proof dropped: %+v %v", got, err)
		}
	}
	for _, status := range []string{generationStatusStale, generationStatusIncomplete, rpcStatusContextCanceled, rpcStatusContextDeadlineExceeded, rpcStatusRejected} {
		data, err := encodeGenerationResponse(generationResponse{Op: generationPage, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = decodeGenerationPage(context.Background(), data, authority.SlotPageRequest{}, presence.NewRecoveryResultBudget(4096, 1<<20)); err == nil {
			t.Fatalf("%s became an empty success", status)
		}
	}
}
