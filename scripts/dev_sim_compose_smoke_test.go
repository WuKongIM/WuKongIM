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

func TestDockerfilePinsSupportedBuildInputsAndAllowsReviewedMirrors(t *testing.T) {
	dockerfile := readFile(t, filepath.Join(repoRoot(t), "Dockerfile"))

	for _, want := range []string{
		"ARG GO_IMAGE=golang:1.26.7-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514",
		"ARG RUNTIME_IMAGE=alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b",
		"ARG GOPROXY=https://goproxy.cn,direct",
		"ENV GOPROXY=${GOPROXY}",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Fatalf("Dockerfile missing injectable build dependency %q", want)
		}
	}
}

func TestDockerfileRunsAsNonRootWithLifecycleContracts(t *testing.T) {
	dockerfile := readFile(t, filepath.Join(repoRoot(t), "Dockerfile"))

	for _, want := range []string{
		"apk upgrade --no-cache",
		"addgroup -S -g 10001 wukongim",
		"adduser -S -D -H -u 10001 -G wukongim",
		"install -d -o wukongim -g wukongim -m 0750 /var/lib/wukongim /var/lib/wkbench /run/wukongim",
		"USER 10001:10001",
		"STOPSIGNAL SIGTERM",
		"HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=12",
		"CMD wget -q --spider -T 5 http://127.0.0.1:5001/readyz || exit 1",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Fatalf("Dockerfile missing non-root runtime contract %q", want)
		}
	}
}

func TestDockerBuildContextExcludesNonRuntimeTrees(t *testing.T) {
	dockerignore := readFile(t, filepath.Join(repoRoot(t), ".dockerignore"))

	for _, want := range []string{"docs", "docs-site", "demo", "web/node_modules"} {
		if !strings.Contains(dockerignore, "\n"+want+"\n") {
			t.Fatalf(".dockerignore missing non-runtime tree %q", want)
		}
	}
}
