package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudDeploymentBundleBuildIsTrustedOfflineAndPreProcurement(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "cloud-deployment-bundle.yml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		"permissions:\n  contents: read",
		"request_id:",
		"if: github.ref == 'refs/heads/main'",
		"path: control",
		"path: source",
		"git -C source merge-base --is-ancestor \"$REQUESTED_SOURCE_SHA\" origin/main",
		"bun install --frozen-lockfile",
		"yarn install --frozen-lockfile",
		"GOOS=linux GOARCH=amd64 go build",
		"source/configs/wkbench/chat-lifecycle/formal.yaml",
		"source/configs/wkbench/chat-lifecycle/rehearsal.yaml",
		"wkcloudbundle\" seal-offline",
		"wkcloudbundle\" verify-offline",
		"sha256sum cloud-deployment-bundle.tar.gz",
		"cloud-deployment-bundle-${{ steps.bundle.outputs.digest_hex }}",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("bundle workflow missing %q", fragment)
		}
	}
	buildAssets := strings.Index(text, "Build Manager and Demo assets on the runner")
	buildGo := strings.Index(text, "Build Linux AMD64 product and control binaries")
	seal := strings.Index(text, "Seal and independently verify content address")
	if buildAssets < 0 || buildGo <= buildAssets || seal <= buildGo {
		t.Fatal("frontend, product build, and sealing order is not fixed")
	}
	for _, forbidden := range []string{
		"id-token: write", "ALIBABA_CLOUD", "secrets.", "quote --plan", " acquire ",
		"wkchatlifecycle:./cmd/wkchatlifecycle",
		"workflow_call:", "push:", "pull_request:", "schedule:",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("bundle workflow unexpectedly contains %q", forbidden)
		}
	}
}

func TestCloudDeploymentToolchainUsesExactChecksums(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "cloud-deployment", "toolchain.env")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, name := range []string{"PROMETHEUS", "NODE_EXPORTER", "CADDY"} {
		if !strings.Contains(text, name+"_VERSION=") || !strings.Contains(text, name+"_LINUX_AMD64_SHA256=") {
			t.Fatalf("toolchain missing exact %s identity", name)
		}
	}
}
