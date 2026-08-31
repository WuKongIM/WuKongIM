package scripts_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDevSimComposeSmokeTrustsStatusCountersForTraffic(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "dev-sim-compose-smoke.sh"))

	if strings.Contains(script, "recent logs do not show simulator traffic markers") ||
		strings.Contains(script, "sim-msg|delivery[.]diag[.]committed_route") {
		t.Fatal("dev-sim smoke should not require debug log traffic markers after /status reports running traffic")
	}
	if !strings.Contains(script, "recent logs contain panic markers") {
		t.Fatal("dev-sim smoke should still fail on panic markers in recent logs")
	}
}

func TestDevSimComposeSmokeDefaultTimeoutCoversHighTrafficStartup(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "dev-sim-compose-smoke.sh"))

	if !strings.Contains(script, `READY_TIMEOUT="${WK_DEV_SIM_READY_TIMEOUT:-180}"`) {
		t.Fatal("default dev-sim smoke timeout should cover the 1000-user Compose startup profile")
	}
	if !strings.Contains(script, "Default: WK_DEV_SIM_READY_TIMEOUT or 180.") {
		t.Fatal("usage text should document the default dev-sim smoke timeout")
	}
}

func TestDevSimComposeSmokeReportsActiveUsers(t *testing.T) {
	script := readFile(t, filepath.Join(repoRoot(t), "scripts", "dev-sim-compose-smoke.sh"))
	if !strings.Contains(script, "active_users") {
		t.Fatal("dev-sim smoke should report active_users from /status")
	}
	if !strings.Contains(script, "(( connected > 0 ))") || !strings.Contains(script, "(( active > 0 ))") {
		t.Fatal("dev-sim smoke should require active users before passing")
	}
}

func TestDockerComposeDevSimDefaultsTargetHighTraffic(t *testing.T) {
	compose := readFile(t, filepath.Join(repoRoot(t), "docker-compose.yml"))

	for _, want := range []string{
		"WK_SIM_USERS: ${WK_SIM_USERS:-1000}",
		"WK_SIM_PERSON_CHANNELS: ${WK_SIM_PERSON_CHANNELS:-500}",
		"WK_SIM_GROUP_CHANNELS: ${WK_SIM_GROUP_CHANNELS:-500}",
		"WK_SIM_GROUP_MEMBERS: ${WK_SIM_GROUP_MEMBERS:-10}",
		"WK_SIM_RATE: ${WK_SIM_RATE:-0.25/s}",
		"WK_SIM_TRAFFIC_CONCURRENCY: ${WK_SIM_TRAFFIC_CONCURRENCY:-128}",
		"WK_SIM_VERIFY_RECV: ${WK_SIM_VERIFY_RECV:-none}",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("docker-compose.yml missing high-traffic dev-sim default %q", want)
		}
	}
}

func TestDockerComposeObservabilityUsesWritableNamedVolumes(t *testing.T) {
	compose := readFile(t, filepath.Join(repoRoot(t), "docker-compose.yml"))

	for _, want := range []string{
		"prometheus-data:/prometheus",
		"grafana-data:/var/lib/grafana",
		"prometheus-data:",
		"grafana-data:",
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("docker-compose.yml missing writable observability volume contract %q", want)
		}
	}
	if strings.Contains(compose, "./docker/dev-observability/") {
		t.Fatal("fresh checkouts must not bind absent observability data directories created as root")
	}
}

func TestDockerfileAllowsReviewedBuildMirrors(t *testing.T) {
	dockerfile := readFile(t, filepath.Join(repoRoot(t), "Dockerfile"))

	for _, want := range []string{
		"ARG GO_IMAGE=golang:1.25.0",
		"ARG RUNTIME_IMAGE=alpine:3.19",
		"ARG GOPROXY=https://goproxy.cn,direct",
		"ENV GOPROXY=${GOPROXY}",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Fatalf("Dockerfile missing injectable build dependency %q", want)
		}
	}
}
