package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// A remote owner that rejects Full Begin must say which check failed before the
// compatible status drops the cause (#1097). The event uses a closed reason and
// numeric claim fields only; raw error text never enters the log.
func TestGenerationOwnerBeginRejectionLogsClosedReason(t *testing.T) {
	claim := wireGeneration()
	cases := []struct {
		name    string
		failure error
		reason  string
	}{
		{"own boot absent", errors.Join(authority.ErrStaleGeneration, fmt.Errorf("private-cause: %w", controller.PresenceRejection(controller.PresenceRejectOwnBootNotInOwnerSet))), "own_boot_not_in_owner_set"},
		{"committed cut conflict", errors.Join(authority.ErrStaleGeneration, controller.ErrPresenceGenerationConflict), "generation_conflict"},
		{"controller unavailable", errors.Join(authority.ErrStaleGeneration, controller.ErrNotLeader), "controller_unavailable"},
		{"owner registry stale", online.ErrStaleActivation, "owner_registry_stale"},
		{"controller read not ready", errors.Join(authority.ErrStaleGeneration, controller.PresenceRejection(controller.PresenceRejectRosterNotCurrent)), "roster_not_current"},
		{"unspecified not ready", errors.Join(authority.ErrStaleGeneration, controller.ErrPresenceGenerationNotReady), "not_ready_unspecified"},
		{"route not ready", errors.Join(authority.ErrStaleGeneration, fmt.Errorf("%w: private-cause", cluster.ErrRouteNotReady)), "route_not_ready"},
		{"node not serving", errors.Join(authority.ErrStaleGeneration, cluster.ErrStopping), "node_not_serving"},
		// Both shapes were observed on Linux for #1097 before they had names.
		{"no slot leader", errors.Join(authority.ErrStaleGeneration, fmt.Errorf("%w: cluster/routing: no slot leader", cluster.ErrNoSlotLeader)), "no_slot_leader"},
		{"transport canceled", errors.Join(authority.ErrStaleGeneration, transport.ErrCanceled), "canceled"},
		{"unknown", errors.New("private-cause"), "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logger := newRecordingNodeLogger("internal.access.node")
			registry := online.NewRegistry(online.RegistryOptions{})
			owner := presence.NewOwnerGenerationWithChecks(registry, 7, 17,
				func(context.Context, presence.GenerationClaim) error { return tc.failure },
				func(context.Context, presence.GenerationClaim) error { return nil })
			adapter := New(Options{GenerationOwner: owner, Logger: logger})
			body, err := encodeGenerationRequest(generationRequest{Op: generationBegin, Claim: claim})
			if err != nil {
				t.Fatal(err)
			}
			reply, err := adapter.HandlePresenceOwnerRPC(context.Background(), body)
			if err != nil {
				t.Fatal(err)
			}
			baseline, err := encodeGenerationResponse(generationResponse{Op: generationBegin, Status: generationStatus(generationOwnerError(tc.failure))})
			if err != nil || !bytes.Equal(reply, baseline) {
				t.Fatalf("diagnostics changed owner response bytes: %v", err)
			}
			entry := requireNodeLogEntry(t, logger, "WARN", "internal.access.node.rpc", "internal.access.node.presence_owner_begin_rejected")
			fields := make(map[string]string)
			for _, field := range entry.fields {
				if field.Key == "error" || field.Key == "cause" {
					t.Fatalf("raw cause field %s added", field.Key)
				}
				value := fmt.Sprint(field.Value)
				if strings.Contains(value, "private-cause") {
					t.Fatalf("raw cause entered %s", field.Key)
				}
				fields[field.Key] = value
			}
			for key, want := range map[string]string{
				"owner_reject_reason": tc.reason,
				"hashSlot":            fmt.Sprint(claim.Target.HashSlot),
				"slotID":              fmt.Sprint(claim.Target.SlotID),
				"leaderNodeID":        fmt.Sprint(claim.Target.LeaderNodeID),
				"leaderTerm":          fmt.Sprint(claim.Target.LeaderTerm),
				"generation":          fmt.Sprint(claim.Generation),
				"ownerSetEpoch":       fmt.Sprint(claim.OwnerSetEpoch),
				"servingBoot":         fmt.Sprint(claim.ServingBoot),
			} {
				if fields[key] != want {
					t.Errorf("%s=%q want %q", key, fields[key], want)
				}
			}
			if len(*logger.entries) != 1 {
				t.Fatal("one rejected Begin produced multiple events")
			}
		})
	}
}

// Page, End and successful Begin keep their existing quiet behavior so the
// added owner event stays bounded by the rare Begin rejection.
func TestGenerationOwnerNonBeginFailureAddsNoEvent(t *testing.T) {
	logger := newRecordingNodeLogger("internal.access.node")
	registry := online.NewRegistry(online.RegistryOptions{})
	owner := presence.NewOwnerGeneration(registry, 7, 17, func(context.Context, presence.GenerationClaim) error { return nil })
	network := &generationLoopback{adapter: New(Options{GenerationOwner: owner, Logger: logger})}
	client := NewClient(network)
	claim := wireGeneration()
	cut, err := client.BeginPresenceSlot(context.Background(), 7, claim)
	if err != nil {
		t.Fatal(err)
	}
	foreign := cut
	foreign.BootID++
	budget := presence.NewRecoveryResultBudget(4096, 1<<20)
	if _, err := client.ReadPresenceSlotPage(context.Background(), presence.SlotPageRequest{Claim: claim, Owner: foreign, Sequence: 1, MaxRows: 16, MaxBytes: 1 << 16}, budget); err == nil {
		t.Fatal("foreign boot page accepted")
	}
	if len(*logger.entries) != 0 {
		t.Fatalf("non-Begin owner failure or success logged %d events", len(*logger.entries))
	}
}

// Precondition failures on the owner itself return the bare stale sentinel;
// they are named separately from port failures that join a cause.
func TestGenerationOwnerBeginInvalidClaimLogsClosedReason(t *testing.T) {
	logger := newRecordingNodeLogger("internal.access.node")
	registry := online.NewRegistry(online.RegistryOptions{})
	owner := presence.NewOwnerGeneration(registry, 7, 17, func(context.Context, presence.GenerationClaim) error { return nil })
	adapter := New(Options{GenerationOwner: owner, Logger: logger})
	claim := wireGeneration()
	claim.ServingBoot = 0
	body, err := encodeGenerationRequest(generationRequest{Op: generationBegin, Claim: claim})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.HandlePresenceOwnerRPC(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	entry := requireNodeLogEntry(t, logger, "WARN", "internal.access.node.rpc", "internal.access.node.presence_owner_begin_rejected")
	for _, field := range entry.fields {
		if field.Key == "owner_reject_reason" && field.Value != "invalid_claim" {
			t.Fatalf("reason=%v want invalid_claim", field.Value)
		}
	}
}
