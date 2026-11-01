package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

// The real generation client/adapter boundary must retain source evidence before
// its compatible deadline status discards the original activation error wrapper.
func TestGenerationActivationDeadlineRetainsBoundedSourceLog(t *testing.T) {
	for _, operation := range []string{"register", "commit"} {
		t.Run(operation, func(t *testing.T) {
			claim, route := wireGeneration(), wireRoute()
			logger := newRecordingNodeLogger("internal.access.node")
			failure := presence.WrapActivationPhase(presence.ActivationPhaseRecoveryProvider,
				fmt.Errorf("private-deadline-cause: %w", context.DeadlineExceeded))
			port := &generationLoggingAuthority{generationAuthorityFixture: generationAuthorityFixture{claim: claim}, failure: failure}
			network := &generationLoopback{adapter: New(Options{GenerationAuthority: port, Logger: logger})}
			client := NewClient(network)
			var err error
			if operation == "register" {
				_, err = client.RegisterPresenceGeneration(context.Background(), claim.Target, route)
			} else {
				_, err = client.CommitPresenceGeneration(context.Background(), claim, "private-deadline-token")
			}
			if !errors.Is(err, context.DeadlineExceeded) || network.calls != 1 {
				t.Fatalf("deadline cause or one-RPC boundary changed: %v / %d calls", err, network.calls)
			}
			entry := requireNodeLogEntry(t, logger, "WARN", "internal.access.node.rpc", "internal.access.node.presence_activation_deadline")
			fields := make(map[string]string)
			for _, field := range entry.fields {
				value := fmt.Sprint(field.Value)
				for _, forbidden := range []string{"uid", "device_id", "deviceID", "session_id", "sessionID", "token", "error", "cause"} {
					if field.Key == forbidden {
						t.Fatalf("private field %s added", field.Key)
					}
				}
				for _, private := range []string{"private-deadline-cause", "private-deadline-token", route.UID} {
					if strings.Contains(value, private) {
						t.Fatalf("private value entered %s", field.Key)
					}
				}
				if strings.HasPrefix(field.Key, "recovery_provider_") {
					t.Fatal("plain deadline acquired an unobserved provider stage")
				}
				fields[field.Key] = value
			}
			for key, want := range map[string]string{
				"activation_phase": "recovery_provider", "status": rpcStatusContextDeadlineExceeded,
				"hashSlot": fmt.Sprint(claim.Target.HashSlot), "slotID": fmt.Sprint(claim.Target.SlotID),
				"leaderNodeID": fmt.Sprint(claim.Target.LeaderNodeID),
			} {
				if fields[key] != want {
					t.Errorf("%s=%q want %q", key, fields[key], want)
				}
			}
			if operation == "register" {
				want := fmt.Sprintf("%x", presence.ActivationRouteHash(route.OwnerNodeID, route.OwnerBootID, route.SessionID))
				if fields["op"] != presenceOpRegisterRoute || fields["activation_route_hash"] != want {
					t.Fatal("original decoded route correlation lost")
				}
			} else if fields["op"] != presenceOpCommitRoute || fields["activation_route_hash"] != "" {
				t.Fatal("token-only Commit invented original route correlation")
			}
			if len(*logger.entries) != 1 {
				t.Fatal("one deadline failure produced multiple events")
			}
		})
	}
}
