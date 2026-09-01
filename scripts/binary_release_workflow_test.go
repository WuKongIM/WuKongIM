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
		"sha256sum --check",
		"docker/setup-qemu-action@",
		"actions/attest@",
		"gh api \"repos/$GITHUB_REPOSITORY/releases/tags/$VERSION\"",
		"cmp \"$RELEASE_DIR/$asset\" \"$existing_dir/$asset\"",
		"already has the complete immutable binary release",
		"gh release create \"$VERSION\"",
		"create_args+=(--prerelease)",
		"gh release upload \"$VERSION\"",
		"wukongim/binary-release-receipt/v1",
		"retention-days: 90",
		"git diff --exit-code HEAD --",
	} {
		require.Contains(t, text, want)
	}

	for _, forbidden := range []string{
		"pull_request:",
		"release:",
		"cancel-in-progress: true",
		"--clobber",
		"windows/",
	} {
		require.NotContains(t, text, forbidden)
	}

	orderedSteps := []string{
		"- name: Validate release identity and tag policy",
		"- name: Build deterministic release archives",
		"- name: Validate binary identities and archive contents",
		"- name: Classify immutable GitHub Release assets",
		"- name: Create GitHub build provenance",
		"- name: Publish absent immutable GitHub Release assets",
		"- name: Verify published Release assets",
		"- name: Write release receipt and summary",
	}
	previous := -1
	for _, step := range orderedSteps {
		position := strings.Index(text, step)
		require.Greater(t, position, previous, step)
		previous = position
	}
}
