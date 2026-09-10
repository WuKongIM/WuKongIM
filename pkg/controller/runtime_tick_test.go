package controller

import (
	"testing"
	"time"
)

func TestRuntimePreservesControllerRaftTimingDefault(t *testing.T) {
	for _, role := range []RuntimeRole{RuntimeRoleVoter, RuntimeRoleMirror} {
		t.Run(string(role), func(t *testing.T) {
			cfg := RuntimeConfig{NodeID: 1, StateDir: "unused", ClusterID: "tick-default", Voters: []Voter{{NodeID: 1, Addr: "node1"}}, Role: role}
			runtime, err := NewRuntime(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.cfg.TickInterval != 100*time.Millisecond {
				t.Fatalf("runtime tick = %s, want canonical Controller Raft 100ms tick (1s election floor)", runtime.cfg.TickInterval)
			}
			cfg.TickInterval = 7 * time.Millisecond
			runtime, err = NewRuntime(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.cfg.TickInterval != cfg.TickInterval {
				t.Fatalf("explicit tick changed: got %s", runtime.cfg.TickInterval)
			}
		})
	}
}
