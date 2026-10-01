//go:build e2e

package send_ban

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
)

// permissionQuorumTraces starts three owned public trace requests and always
// joins them, including failure paths. Headroom is not proof of capture start;
// the offline lineage contract requires every target proposal in the raw trace.
func permissionQuorumTraces(ctx context.Context, cluster *suite.StartedCluster, path string) func() []permissionSequentialProfile {
	done := make(chan permissionSequentialProfile, 3)
	for _, node := range cluster.Nodes {
		go func(id uint64, addr string) {
			out := permissionSequentialProfile{NodeID: id, Kind: "trace", Seconds: 4, Path: fmt.Sprintf("%s.node-%d.trace", path, id), Requested: time.Now().UTC()}
			if err := permissionQuorumTrace(ctx, addr, out.Path); err != nil {
				out.ErrorCode = "capture_unavailable_or_oversized"
			}
			out.Returned = time.Now().UTC()
			done <- out
		}(node.Spec.ID, node.APIAddr())
	}
	var once sync.Once
	var captures []permissionSequentialProfile
	join := func() []permissionSequentialProfile {
		once.Do(func() {
			for len(captures) < 3 {
				captures = append(captures, <-done)
			}
		})
		return captures
	}
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
	return join
}

func permissionQuorumTrace(parent context.Context, addr, path string) error {
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/debug/pprof/trace?seconds=4", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("trace HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	if err != nil {
		return err
	}
	if len(body) == 0 || len(body) > 16<<20 {
		return fmt.Errorf("trace outside byte bound")
	}
	return os.WriteFile(path, body, 0644)
}
