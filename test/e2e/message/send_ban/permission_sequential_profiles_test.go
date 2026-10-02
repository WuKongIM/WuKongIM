//go:build e2e

package send_ban

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

// Profile request bounds are retained separately from traffic bounds. The
// public endpoints do not certify the instant sampling starts or full overlap.
type permissionSequentialProfile struct {
	NodeID    uint64    `json:"node_id"`
	Kind      string    `json:"kind"`
	Path      string    `json:"path"`
	Seconds   int       `json:"seconds"`
	Requested time.Time `json:"requested_at"`
	Returned  time.Time `json:"returned_at"`
	ErrorCode string    `json:"error_code,omitempty"`
}

// permissionSequentialProfiles joins six bounded public captures before
// returning, including failed requests. Its only traffic is a separate phase.
func permissionSequentialProfiles(ctx context.Context, cluster *suite.StartedCluster, path string, traffic func() []permissionBaselineAck) ([]permissionBaselineAck, []permissionSequentialProfile) {
	done := make(chan permissionSequentialProfile, 6)
	for _, node := range cluster.Nodes {
		for _, kind := range []string{"cpu", "allocs"} {
			go func(id uint64, addr, kind string) {
				out := permissionSequentialProfile{NodeID: id, Kind: kind, Seconds: 4, Path: fmt.Sprintf("%s.node-%d.%s.pprof", path, id, kind), Requested: time.Now().UTC()}
				endpoint := "/debug/pprof/allocs?seconds=4"
				if kind == "cpu" {
					endpoint = "/debug/pprof/profile?seconds=4"
				}
				if err := permissionBaselineProfile(ctx, addr, endpoint, out.Path); err != nil {
					out.ErrorCode = "capture_unavailable_or_oversized"
				}
				out.Returned = time.Now().UTC()
				done <- out
			}(node.Spec.ID, node.APIAddr(), kind)
		}
	}
	acks := traffic()
	profiles := make([]permissionSequentialProfile, 0, 6)
	for len(profiles) < 6 {
		profiles = append(profiles, <-done)
	}
	return acks, profiles
}
