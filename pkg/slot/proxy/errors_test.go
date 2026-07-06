package proxy

import (
	"errors"
	"fmt"
	"testing"
)

func TestRouteErrorMatchesCanonicalAliases(t *testing.T) {
	tests := []struct {
		name     string
		sentinel *routeError
		alias    string
	}{
		{name: "no leader cluster", sentinel: ErrNoLeader, alias: "cluster: no slot leader"},
		{name: "no leader routing", sentinel: ErrNoLeader, alias: "cluster/routing: no slot leader"},
		{name: "not leader cluster", sentinel: ErrNotLeader, alias: "cluster: not leader"},
		{name: "not leader propose", sentinel: ErrNotLeader, alias: "cluster/propose: not leader"},
		{name: "slot not found", sentinel: ErrSlotNotFound, alias: "cluster: slot not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New(tt.alias)
			if !tt.sentinel.Is(err) {
				t.Fatalf("sentinel should match exact alias %q", tt.alias)
			}
			if !routeErrorMatches(err, tt.sentinel) {
				t.Fatalf("routeErrorMatches should match exact alias %q", tt.alias)
			}
			if !routeErrorMatches(fmt.Errorf("remote propose failed: %s", tt.alias), tt.sentinel) {
				t.Fatalf("routeErrorMatches should match wrapped alias %q", tt.alias)
			}
		})
	}
}

func TestRouteErrorDoesNotMatchRemovedLegacyAliases(t *testing.T) {
	tests := []struct {
		name     string
		sentinel *routeError
		alias    string
	}{
		{name: "legacy no leader", sentinel: ErrNoLeader, alias: "raftcluster: no leader for slot"},
		{name: "legacy not leader", sentinel: ErrNotLeader, alias: "raftcluster: not leader"},
		{name: "legacy slot not found", sentinel: ErrSlotNotFound, alias: "raftcluster: slot not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.New(tt.alias)
			if tt.sentinel.Is(err) {
				t.Fatalf("sentinel should not match removed legacy alias %q", tt.alias)
			}
			if routeErrorMatches(err, tt.sentinel) {
				t.Fatalf("routeErrorMatches should not match removed legacy alias %q", tt.alias)
			}
		})
	}
}
