package docker_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestComposeNodesEnableMetricsByDefault(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	repoRoot := filepath.Dir(filepath.Dir(filename))
	body, err := os.ReadFile(filepath.Join(repoRoot, "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}

	metricsDefaultEnabled := regexp.MustCompile(`WK_METRICS_ENABLE:\s*\$\{WK_METRICS_ENABLE:-true\}`).Match(body)
	if metricsDefaultEnabled {
		return
	}

	for _, node := range []string{"node1.conf", "node2.conf", "node3.conf"} {
		confPath := filepath.Join(repoRoot, "docker", "conf", node)
		confBody, err := os.ReadFile(confPath)
		if err != nil {
			t.Fatalf("read %s: %v", confPath, err)
		}
		if !strings.Contains(string(confBody), "WK_METRICS_ENABLE=true") {
			t.Fatalf("%s should enable WK_METRICS_ENABLE by default so manager dashboard metrics collectors start", confPath)
		}
	}
}

func TestComposeNodeConfigsUseExplicitDataPlaneConcurrency(t *testing.T) {
	repoRoot := dockerRepoRoot(t)
	for _, node := range []string{"node1.conf", "node2.conf", "node3.conf"} {
		confPath := filepath.Join(repoRoot, "docker", "conf", node)
		confBody, err := os.ReadFile(confPath)
		if err != nil {
			t.Fatalf("read %s: %v", confPath, err)
		}
		conf := string(confBody)
		for _, want := range []string{
			"WK_CLUSTER_DATA_PLANE_POOL_SIZE=8",
			"WK_CLUSTER_DATA_PLANE_MAX_FETCH_INFLIGHT=16",
			"WK_CLUSTER_DATA_PLANE_MAX_PENDING_FETCH=16",
		} {
			if !strings.Contains(conf, want) {
				t.Fatalf("%s should contain %s for dev-sim high-traffic data-plane headroom", confPath, want)
			}
		}
	}
}

func TestComposeNodeConfigsUseExplicitGatewayGnetHeadroom(t *testing.T) {
	repoRoot := dockerRepoRoot(t)
	for _, node := range []string{"node1.conf", "node2.conf", "node3.conf"} {
		confPath := filepath.Join(repoRoot, "docker", "conf", node)
		confBody, err := os.ReadFile(confPath)
		if err != nil {
			t.Fatalf("read %s: %v", confPath, err)
		}
		conf := string(confBody)
		for _, want := range []string{
			"WK_GATEWAY_GNET_MULTICORE=true",
			"WK_GATEWAY_GNET_NUM_EVENT_LOOP=4",
			"WK_GATEWAY_GNET_REUSE_PORT=true",
			"WK_GATEWAY_GNET_READ_BUFFER_CAP=8192",
			"WK_GATEWAY_GNET_WRITE_BUFFER_CAP=16384",
		} {
			if !strings.Contains(conf, want) {
				t.Fatalf("%s should contain %s for dev-sim gateway ingress headroom", confPath, want)
			}
		}
	}
}

func dockerRepoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	return filepath.Dir(filepath.Dir(filename))
}
