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
