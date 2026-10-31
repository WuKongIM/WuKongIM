package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

// The configured membership endpoint may be a relay or NAT address while the
// transport listens elsewhere. Self proof must bind that configured endpoint,
// not an arbitrary replacement or an alternate local listener address.
func TestPresenceSelfCapabilityUsesConfiguredMembershipEndpoint(t *testing.T) {
	const listener = "127.0.0.1:17001"
	const advertised = "127.0.0.1:27001"
	for _, tc := range []struct {
		name     string
		config   Config
		accepted string
		rejected []string
	}{
		{
			name:     "implicit_single_node_cluster_listener",
			config:   Config{NodeID: 1, ListenAddr: listener},
			accepted: listener,
			rejected: []string{advertised, "127.0.0.1:37001", ""},
		},
		{
			name: "static_voter_configured_relay",
			config: Config{NodeID: 1, ListenAddr: listener, Control: ControlConfig{
				Role:   ControlRoleVoter,
				Voters: []ControlVoter{{NodeID: 2, Addr: "127.0.0.1:27002"}, {NodeID: 1, Addr: advertised}},
			}},
			accepted: advertised,
			rejected: []string{listener, "127.0.0.1:27002", "127.0.0.1:37001", ""},
		},
		{
			name: "dynamic_join_configured_advertise",
			config: Config{NodeID: 1, ListenAddr: listener,
				Control: ControlConfig{Role: ControlRoleMirror},
				Join:    JoinConfig{Seeds: []string{"127.0.0.1:27002"}, AdvertiseAddr: advertised, Token: "fixture-only-join"},
			},
			accepted: advertised,
			rejected: []string{listener, "127.0.0.1:27002", "127.0.0.1:37001", ""},
		},
		{
			name: "static_mirror_without_self_voter_uses_listener",
			config: Config{NodeID: 1, ListenAddr: listener, Control: ControlConfig{
				Role: ControlRoleMirror, Voters: []ControlVoter{{NodeID: 2, Addr: "127.0.0.1:27002"}},
			}},
			accepted: listener,
			rejected: []string{advertised, "127.0.0.1:27002", "127.0.0.1:37001", ""},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &Node{cfg: tc.config, presenceBoot: 123}
			if err := node.EnablePresenceProtocol(controller.PresenceProtocolVersion); err != nil {
				t.Fatal(err)
			}
			want := node.PresenceCapabilities()
			t.Run("exact_endpoint_keeps_actual_boot_proof", func(t *testing.T) {
				got, err := node.probePresenceCapabilities(context.Background(), tc.config.NodeID, tc.accepted)
				if err != nil || got != want {
					t.Fatalf("configured self capability = %+v, %v; want %+v", got, err, want)
				}
			})
			for _, address := range tc.rejected {
				t.Run("reject_"+address, func(t *testing.T) {
					got, err := node.probePresenceCapabilities(context.Background(), tc.config.NodeID, address)
					if !errors.Is(err, controller.ErrPresenceGenerationNotReady) || got != (controller.PresenceCapabilities{}) {
						t.Fatalf("unconfigured self capability = %+v, %v; want no proof and not-ready", got, err)
					}
				})
			}
		})
	}
}
