package scripts_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestDockerImagePublishWorkflowContract(t *testing.T) {
	raw := readWorkflow(t, "docker-image-publish.yml")
	text := string(raw)

	var workflow struct {
		On struct {
			Push struct {
				Tags []string `yaml:"tags"`
			} `yaml:"push"`
			WorkflowDispatch struct {
				Inputs map[string]struct {
					Required bool   `yaml:"required"`
					Type     string `yaml:"type"`
				} `yaml:"inputs"`
			} `yaml:"workflow_dispatch"`
		} `yaml:"on"`
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			If             string `yaml:"if"`
			RunsOn         string `yaml:"runs-on"`
			TimeoutMinutes int    `yaml:"timeout-minutes"`
			Environment    string `yaml:"environment"`
			Steps          []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &workflow))
	require.Equal(t, []string{"v*"}, workflow.On.Push.Tags)
	versionInput, ok := workflow.On.WorkflowDispatch.Inputs["version"]
	require.True(t, ok)
	require.True(t, versionInput.Required)
	require.Equal(t, "string", versionInput.Type)
	require.Equal(t, map[string]string{
		"attestations": "write",
		"contents":     "read",
		"id-token":     "write",
		"packages":     "write",
	}, workflow.Permissions)
	publish, ok := workflow.Jobs["publish"]
	require.True(t, ok)
	require.Equal(t, "github.repository == 'WuKongIM/WuKongIM'", publish.If)
	require.Equal(t, "ubuntu-24.04", publish.RunsOn)
	require.Equal(t, 90, publish.TimeoutMinutes)
	require.Equal(t, "docker-publish", publish.Environment)

	for _, want := range []string{
		"ghcr.io/wukongim/wukongim",
		"docker.io/wukongim/wukongim",
		"registry.cn-shanghai.aliyuncs.com/wukongim/wukongim",
		"vars.DOCKERHUB_USERNAME",
		"secrets.DOCKERHUB_TOKEN",
		"vars.ALIYUNHUB_USERNAME",
		"secrets.ALIYUNHUB_TOKEN",
		"persist-credentials: false",
		"git show-ref --verify --quiet \"refs/tags/$version\"",
		"git merge-base --is-ancestor \"$source_sha\" origin/main",
		"scripts/extract-release-notes.awk",
		"CHANGELOG.md",
		"SemVer build metadata is not representable as an OCI tag",
		"version: v0.36.1",
		"platforms: linux/amd64,linux/arm64",
		"provenance: false",
		"sbom: false",
		"cache-from: type=gha,scope=docker-release",
		"cache-to: type=gha,mode=max,scope=docker-release",
		"aquasecurity/setup-trivy@",
		"version: v0.74.0",
		"wukongim-release-scan:amd64",
		"wukongim-release-scan:arm64",
		"--severity CRITICAL,HIGH",
		"--exit-code 1",
		`--platform "linux/$arch"`,
		"anchore/sbom-action/download-syft@",
		"syft-version: v1.51.1",
		`--platform "linux/$arch"`,
		`--output "cyclonedx-json=$output"`,
		"actions/attest-build-provenance@",
		"actions/attest@",
		"sbom-linux-amd64.cdx.json",
		"sbom-linux-arm64.cdx.json",
		"canonical GHCR tag is absent",
		"$CANONICAL_IMAGE@$CANONICAL_DIGEST",
		`inspect_ref "$ref" mirror.json mirror.err || status=$?`,
		`case "$status" in`,
		"inspect_with_retry",
		".manifest.digest == $digest",
		`["amd64", "arm64"]`,
		"$major.$minor",
		"aliases+=(\"$major\")",
		"aliases+=(latest)",
		"wukongim/docker-release-receipt/v1",
		"retention-days: 90",
		"git diff --exit-code HEAD --",
	} {
		require.Contains(t, text, want)
	}

	for _, forbidden := range []string{
		"pull_request:",
		"release:",
		"cancel-in-progress: true",
		"push-to-registry: true",
		"install: true",
		"provenance: mode=max",
		"sbom: true",
	} {
		require.NotContains(t, text, forbidden)
	}

	orderedSteps := []string{
		"- name: Validate release identity and tag policy",
		"- name: Validate changelog release notes",
		"- name: Validate registry credentials",
		"- name: Classify immutable publication state",
		"- name: Build amd64 security scan candidate",
		"- name: Build arm64 security scan candidate",
		"- name: Set up Trivy",
		"- name: Scan local release candidates",
		"- name: Scan recovered canonical image",
		"- name: Build and push canonical image",
		"- name: Verify canonical digest and platform manifests",
		"- name: Generate per-platform SBOMs",
		"- name: Create GitHub build attestation",
		"- name: Create GitHub amd64 SBOM attestation",
		"- name: Create GitHub arm64 SBOM attestation",
		"- name: Mirror immutable exact tag",
		"- name: Verify exact digest and platform manifests",
		"- name: Update eligible floating tags",
		"- name: Write release receipt and summary",
	}
	previous := -1
	for _, step := range orderedSteps {
		position := strings.Index(text, step)
		require.Greater(t, position, previous, step)
		previous = position
	}

	changelogRun := ""
	for _, step := range publish.Steps {
		if step.Name == "Validate changelog release notes" {
			changelogRun = step.Run
			break
		}
	}
	require.Contains(t, changelogRun, `awk -v version="$VERSION"`)
	require.Contains(t, changelogRun, `scripts/extract-release-notes.awk CHANGELOG.md`)
	require.Contains(t, changelogRun, `test -s "$notes_path"`)
}
