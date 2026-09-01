package scripts_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestBinaryReleaseWorkflowContract(t *testing.T) {
	raw := readWorkflow(t, "binary-release-publish.yml")
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
				Name string            `yaml:"name"`
				If   string            `yaml:"if"`
				Uses string            `yaml:"uses"`
				Run  string            `yaml:"run"`
				With map[string]string `yaml:"with"`
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
		"contents":     "write",
		"id-token":     "write",
	}, workflow.Permissions)
	publish, ok := workflow.Jobs["publish"]
	require.True(t, ok)
	require.Equal(t, "github.repository == 'WuKongIM/WuKongIM'", publish.If)
	require.Equal(t, "ubuntu-24.04", publish.RunsOn)
	require.Equal(t, 60, publish.TimeoutMinutes)
	require.Equal(t, "binary-publish", publish.Environment)

	for _, want := range []string{
		"persist-credentials: false",
		"git show-ref --verify --quiet \"refs/tags/$version\"",
		"git merge-base --is-ancestor \"$source_sha\" origin/main",
		"release policy does not allow SemVer build metadata",
		"CGO_ENABLED=0 GOOS=\"$goos\" GOARCH=\"$goarch\"",
		"go build -trimpath -buildvcs=false",
		"-X main.buildVersion=$BINARY_VERSION",
		"-X main.buildCommit=$SOURCE_SHA",
		"-X main.buildSource=release",
		"linux/amd64",
		"linux/arm64",
		"darwin/amd64",
		"darwin/arm64",
		"tar --sort=name",
		"gzip -n -9",
		"goreleaser/goreleaser-action@4c6ab561adb47e50c45ef534e2155934e91c40c1",
		"version: v2.18.0",
		"args: release --clean --config .goreleaser.packages.yaml",
		"wukongim_${BINARY_VERSION}_linux_amd64.deb",
		"wukongim_${BINARY_VERSION}_linux_amd64.rpm",
		"dpkg-deb --field",
		"sha256sum \"${release_assets[@]}\"",
		"sha256sum --check",
		"docker/setup-qemu-action@",
		"actions/attest@",
		"gh api \"repos/$GITHUB_REPOSITORY/immutable-releases\"",
		"repository immutable Releases must remain enabled before publication",
		"gh api --paginate --slurp",
		"repos/$GITHUB_REPOSITORY/releases?per_page=100",
		"select(.tag_name == $version)",
		"resolves to multiple GitHub Releases",
		"release_id=$release_id",
		"repos/$GITHUB_REPOSITORY/releases/assets/$asset_id",
		"cmp \"$RELEASE_DIR/$asset\" \"$existing_dir/$asset\"",
		"already has the complete published binary release",
		"already published but incomplete; refusing to reuse its tag; publish a new SemVer tag",
		"Release contains unexpected asset",
		"git ls-remote origin",
		"remote_source_sha=\"${peeled_tag_sha:-$raw_tag_sha}\"",
		"gh api --method POST \"repos/$GITHUB_REPOSITORY/releases\"",
		"generate_release_notes: true",
		"--input \"$create_request\"",
		"https://uploads.github.com/repos/$GITHUB_REPOSITORY/releases/$release_id/assets{?name,label}",
		"curl --fail-with-body --silent --show-error",
		"$upload_endpoint?name=$asset",
		"upload response does not match the local asset",
		"must remain mutable and draft until all assets are verified",
		"draft does not contain the exact expected asset set",
		"repository immutable Releases were disabled before publication",
		"gh api --method PATCH",
		"-F draft=false",
		".tag_name == $version and .draft == false",
		".immutable == true",
		"published Release does not contain the exact expected asset set",
		"api_digest=\"$(jq -r '.digest // empty'",
		"local_size=\"$(stat -c '%s'",
		"$api_digest\" == \"sha256:$local_sha256",
		"wukongim/binary-release-receipt/v1",
		"unsigned_native_packages: ($native_packages == \"true\")",
		"retention-days: 90",
		"git diff --exit-code HEAD --",
	} {
		require.Contains(t, text, want)
	}

	for _, forbidden := range []string{
		"pull_request:",
		"\n  release:",
		"cancel-in-progress: true",
		"--clobber",
		"delete and recreate the exact Release",
		"releases/tags/",
		"windows/",
		"--generate-notes",
	} {
		require.NotContains(t, text, forbidden)
	}
	require.Equal(t, 2, strings.Count(text, "git ls-remote origin"))
	require.Equal(t, 2, strings.Count(text, "repos/$GITHUB_REPOSITORY/immutable-releases"))

	orderedSteps := []string{
		"- name: Validate release identity and tag policy",
		"- name: Build deterministic release archives",
		"- name: Build unsigned amd64 native packages",
		"- name: Normalize unsigned native package assets",
		"- name: Finalize exact release asset set and checksums",
		"- name: Validate binary identities and archive contents",
		"- name: Classify draft GitHub Release assets",
		"- name: Create GitHub build provenance",
		"- name: Create or recover draft GitHub Release",
		"- name: Verify complete draft Release",
		"- name: Publish verified draft Release once",
		"- name: Verify published immutable Release",
		"- name: Write release receipt and summary",
	}
	previous := -1
	for _, step := range orderedSteps {
		position := strings.Index(text, step)
		require.Greater(t, position, previous, step)
		previous = position
	}

	stepRuns := make(map[string]string, len(publish.Steps))
	stepUses := make(map[string]string, len(publish.Steps))
	stepIfs := make(map[string]string, len(publish.Steps))
	stepWith := make(map[string]map[string]string, len(publish.Steps))
	var packageBuildStep struct {
		If   string
		Uses string
		With map[string]string
	}
	for _, step := range publish.Steps {
		stepRuns[step.Name] = step.Run
		stepUses[step.Name] = step.Uses
		stepIfs[step.Name] = step.If
		stepWith[step.Name] = step.With
		if step.Name == "Build unsigned amd64 native packages" {
			packageBuildStep.If = step.If
			packageBuildStep.Uses = step.Uses
			packageBuildStep.With = step.With
		}
	}
	require.Equal(t, "steps.build.outputs.native_packages == 'true'", packageBuildStep.If)
	require.Equal(t, "goreleaser/goreleaser-action@4c6ab561adb47e50c45ef534e2155934e91c40c1", packageBuildStep.Uses)
	require.Equal(t, map[string]string{
		"distribution": "goreleaser",
		"version":      "v2.18.0",
		"args":         "release --clean --config .goreleaser.packages.yaml",
	}, packageBuildStep.With)

	finalizeRun := stepRuns["Finalize exact release asset set and checksums"]
	require.Contains(t, finalizeRun, "find \"$RELEASE_DIR\" -maxdepth 1 -type f")
	require.Contains(t, finalizeRun, "sha256sum \"${release_assets[@]}\" | sort -k2")
	require.Contains(t, finalizeRun, "sha256sum --check \"$CHECKSUM_NAME\"")
	require.GreaterOrEqual(t, strings.Count(text, "\"wukongim_${BINARY_VERSION}_linux_amd64.deb\""), 5)
	require.GreaterOrEqual(t, strings.Count(text, "\"wukongim_${BINARY_VERSION}_linux_amd64.rpm\""), 5)
	require.Equal(t, 3, strings.Count(text, "\"$RELEASE_DIR\"/*.deb"))
	require.Equal(t, 3, strings.Count(text, "\"$RELEASE_DIR\"/*.rpm"))
	for _, stepName := range []string{
		"Classify draft GitHub Release assets",
		"Verify complete draft Release",
		"Publish verified draft Release once",
		"Verify published immutable Release",
	} {
		run := stepRuns[stepName]
		require.Contains(t, run, "wukongim_${BINARY_VERSION}_linux_amd64.deb", stepName)
		require.Contains(t, run, "wukongim_${BINARY_VERSION}_linux_amd64.rpm", stepName)
		require.Contains(t, run, "if [[ \"$NATIVE_PACKAGES\" == true ]]", stepName)
	}
	for _, stepName := range []string{
		"Verify complete draft Release",
		"Publish verified draft Release once",
		"Verify published immutable Release",
	} {
		run := stepRuns[stepName]
		require.Contains(t, run, "\"$RELEASE_DIR\"/*.deb", stepName)
		require.Contains(t, run, "\"$RELEASE_DIR\"/*.rpm", stepName)
	}
	require.Equal(t, "actions/attest@1e69f48acb82d1966a394da916b4c1698aa569d6", stepUses["Create GitHub build provenance"])
	require.Contains(t, stepWith["Create GitHub build provenance"]["subject-path"], "/*.deb")
	require.Contains(t, stepWith["Create GitHub build provenance"]["subject-path"], "/*.rpm")
	require.Contains(t, stepWith["Upload binary release evidence"]["path"], "/*.deb")
	require.Contains(t, stepWith["Upload binary release evidence"]["path"], "/*.rpm")
	require.Empty(t, stepIfs["Finalize exact release asset set and checksums"])
	classifyRun := stepRuns["Classify draft GitHub Release assets"]
	require.Contains(t, classifyRun, "gh api --paginate --slurp")
	require.Contains(t, classifyRun, "[.[][] | select(.tag_name == $version)]")
	require.Contains(t, classifyRun, "release_id=\"$(jq -r '.id'")
	require.NotContains(t, classifyRun, "releases/tags/")

	createRun := stepRuns["Create or recover draft GitHub Release"]
	require.Contains(t, createRun, "release_id=\"$(jq -r '.id' \"$created_release\")\"")
	require.Contains(t, createRun, "repos/$GITHUB_REPOSITORY/releases/$release_id")
	require.Contains(t, createRun, "expected_upload_url=\"https://uploads.github.com/")
	require.Contains(t, createRun, "upload_endpoint=\"${upload_url%%\\{*}\"")
	require.Contains(t, createRun, "\"$upload_endpoint?name=$asset\"")
	require.Contains(t, createRun, ".size == $size and .digest == $digest")
	require.Contains(t, createRun, "generate_release_notes: true")
	require.Contains(t, createRun, "--input \"$create_request\"")
	require.NotContains(t, createRun, "gh release")
	require.NotContains(t, createRun, "repos/$GITHUB_REPOSITORY/releases/$release_id/assets?name=$asset")

	verifyDraftRun := stepRuns["Verify complete draft Release"]
	require.Contains(t, verifyDraftRun, "releases/$RELEASE_ID")
	require.Contains(t, verifyDraftRun, "releases/assets/$asset_id")
	require.NotContains(t, verifyDraftRun, "releases/tags/")
	require.NotContains(t, verifyDraftRun, "--method PATCH")

	publishRun := stepRuns["Publish verified draft Release once"]
	tagCheck := strings.Index(publishRun, "git ls-remote origin")
	immutableCheck := strings.Index(publishRun, "immutable-releases")
	assetSetCheck := strings.Index(publishRun, "draft asset set changed immediately before publication")
	digestCheck := strings.Index(publishRun, "draft digest changed immediately before publication")
	prereleaseCheck := strings.Index(publishRun, "draft prerelease classification changed before publication")
	publishCall := strings.Index(publishRun, "gh api --method PATCH")
	require.GreaterOrEqual(t, tagCheck, 0)
	require.Greater(t, immutableCheck, tagCheck)
	require.Greater(t, prereleaseCheck, immutableCheck)
	require.Greater(t, assetSetCheck, prereleaseCheck)
	require.Greater(t, digestCheck, assetSetCheck)
	require.Greater(t, publishCall, digestCheck)
	require.Contains(t, publishRun, "releases/$RELEASE_ID")
	require.Contains(t, publishRun, "stat -c '%s'")
	require.Contains(t, publishRun, "sha256:$local_sha256")
	require.Contains(t, publishRun, ">\"$prepublish_dir/$asset\"")
	require.Contains(t, publishRun, "cmp \"$local_asset\" \"$prepublish_dir/$asset\"")

	verifyPublishedRun := stepRuns["Verify published immutable Release"]
	require.Contains(t, verifyPublishedRun, "releases/$RELEASE_ID")
	require.Contains(t, verifyPublishedRun, ".digest // empty")
	require.Contains(t, verifyPublishedRun, "stat -c '%s'")
	require.Contains(t, verifyPublishedRun, "sha256:$local_sha256")
	require.Contains(t, verifyPublishedRun, "releases/assets/$asset_id")
	require.Contains(t, verifyPublishedRun, ">\"$published_dir/$asset\"")
	require.Contains(t, verifyPublishedRun, "cmp \"$local_asset\" \"$published_dir/$asset\"")
	require.NotContains(t, verifyPublishedRun, "releases/tags/")
}
