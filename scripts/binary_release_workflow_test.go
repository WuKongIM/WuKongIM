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
		"actions":      "read",
		"attestations": "write",
		"contents":     "write",
		"id-token":     "write",
	}, workflow.Permissions)
	publish, ok := workflow.Jobs["publish"]
	require.True(t, ok)
	require.Equal(t, "github.repository == 'WuKongIM/WuKongIM'", publish.If)
	require.Equal(t, "ubuntu-24.04", publish.RunsOn)
	require.Equal(t, 120, publish.TimeoutMinutes)
	require.Equal(t, "binary-publish", publish.Environment)

	for _, want := range []string{
		"persist-credentials: false",
		"git show-ref --verify --quiet \"refs/tags/$version\"",
		"git merge-base --is-ancestor \"$source_sha\" origin/main",
		"scripts/extract-release-notes.awk",
		"CHANGELOG.md",
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
		"sha256sum --check",
		"docker/setup-qemu-action@",
		"docker/setup-buildx-action@",
		"actions/workflows/docker-image-publish.yml/runs?per_page=100",
		"No successful Docker publication workflow for $VERSION completed within 40 minutes",
		"Docker images for $VERSION were not complete in all registries within 5 minutes after the successful workflow",
		"org.opencontainers.image.revision",
		"org.opencontainers.image.version",
		"Release artifacts / 发布产物",
		"Full Changelog",
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
		"--rawfile body \"$EXPECTED_BODY\"",
		"--input \"$create_request\"",
		"https://uploads.github.com/repos/$GITHUB_REPOSITORY/releases/$release_id/assets{?name,label}",
		"curl --fail-with-body --silent --show-error",
		"$upload_endpoint?name=$asset",
		"upload response does not match the local asset",
		"must retain the exact generated body and remain mutable and draft until all assets are verified",
		"draft does not contain the exact expected asset set",
		"repository immutable Releases were disabled before publication",
		"gh api --method PATCH",
		"-F draft=false",
		".tag_name == $version and .draft == false",
		".immutable == true and .body == $expected",
		"published Release does not contain the exact expected asset set",
		"api_digest=\"$(jq -r '.digest // empty'",
		"local_size=\"$(stat -c '%s'",
		"$api_digest\" == \"sha256:$local_sha256",
		"wukongim/binary-release-receipt/v1",
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
		"generate_release_notes",
		"--generate-notes",
	} {
		require.NotContains(t, text, forbidden)
	}
	require.Equal(t, 2, strings.Count(text, "git ls-remote origin"))
	require.Equal(t, 2, strings.Count(text, "repos/$GITHUB_REPOSITORY/immutable-releases"))

	orderedSteps := []string{
		"- name: Validate release identity and tag policy",
		"- name: Validate and extract changelog release notes",
		"- name: Build deterministic release archives",
		"- name: Validate binary identities and archive contents",
		"- name: Verify published Docker images and render Release body",
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
	for _, step := range publish.Steps {
		stepRuns[step.Name] = step.Run
	}
	releaseBodyRun := stepRuns["Verify published Docker images and render Release body"]
	require.Contains(t, releaseBodyRun, "actions/workflows/docker-image-publish.yml/runs?per_page=100")
	require.Contains(t, releaseBodyRun, `.event == "push" and .head_branch == $version and .head_sha == $source`)
	require.Contains(t, releaseBodyRun, `.status == "completed" and .conclusion == "success"`)
	require.Contains(t, releaseBodyRun, `org.opencontainers.image.revision`)
	require.Contains(t, releaseBodyRun, `org.opencontainers.image.version`)
	require.Contains(t, releaseBodyRun, `== ["amd64", "arm64"]`)
	require.Contains(t, releaseBodyRun, `cat "$CHANGELOG_NOTES"`)
	require.Contains(t, releaseBodyRun, `echo "body_path=$release_body"`)

	classifyRun := stepRuns["Classify draft GitHub Release assets"]
	require.Contains(t, classifyRun, "gh api --paginate --slurp")
	require.Contains(t, classifyRun, "[.[][] | select(.tag_name == $version)]")
	require.Contains(t, classifyRun, "release_id=\"$(jq -r '.id'")
	require.Contains(t, classifyRun, "--rawfile expected \"$EXPECTED_BODY\"")
	require.Contains(t, classifyRun, ".body == $expected")
	require.NotContains(t, classifyRun, "releases/tags/")

	createRun := stepRuns["Create or recover draft GitHub Release"]
	require.Contains(t, createRun, "release_id=\"$(jq -r '.id' \"$created_release\")\"")
	require.Contains(t, createRun, "repos/$GITHUB_REPOSITORY/releases/$release_id")
	require.Contains(t, createRun, "expected_upload_url=\"https://uploads.github.com/")
	require.Contains(t, createRun, "upload_endpoint=\"${upload_url%%\\{*}\"")
	require.Contains(t, createRun, "\"$upload_endpoint?name=$asset\"")
	require.Contains(t, createRun, ".size == $size and .digest == $digest")
	require.Contains(t, createRun, "--rawfile body \"$EXPECTED_BODY\"")
	require.Contains(t, createRun, "--input \"$create_request\"")
	require.Contains(t, createRun, ".body == $expected")
	require.NotContains(t, createRun, "gh release")
	require.NotContains(t, createRun, "repos/$GITHUB_REPOSITORY/releases/$release_id/assets?name=$asset")

	verifyDraftRun := stepRuns["Verify complete draft Release"]
	require.Contains(t, verifyDraftRun, "releases/$RELEASE_ID")
	require.Contains(t, verifyDraftRun, "releases/assets/$asset_id")
	require.Contains(t, verifyDraftRun, ".body == $expected")
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
	require.Contains(t, publishRun, ".body == $expected")

	verifyPublishedRun := stepRuns["Verify published immutable Release"]
	require.Contains(t, verifyPublishedRun, "releases/$RELEASE_ID")
	require.Contains(t, verifyPublishedRun, ".digest // empty")
	require.Contains(t, verifyPublishedRun, "stat -c '%s'")
	require.Contains(t, verifyPublishedRun, "sha256:$local_sha256")
	require.Contains(t, verifyPublishedRun, "releases/assets/$asset_id")
	require.Contains(t, verifyPublishedRun, ".body == $expected")
	require.NotContains(t, verifyPublishedRun, "releases/tags/")
}
