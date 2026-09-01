package scripts_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReleaseNotesWorkflowExtractsExactVersionSection(t *testing.T) {
	changelog := `# Changelog

## [Unreleased]

### 🚀 New Features / 新功能

- Pending change.

## [v3.0.0-beta.6] - 2026-09-02

### 🐛 Bug Fixes / 问题修复

- Preserve ` + "`inline code`" + ` and 中文.

` + "```toml" + `
message = "## headings inside fences are data"
` + "```" + `

## [v3.0.0-beta.5] - 2026-09-01

### 🔧 Improvements / 改进

- Earlier release.
`

	want := `### 🐛 Bug Fixes / 问题修复

- Preserve ` + "`inline code`" + ` and 中文.

` + "```toml" + `
message = "## headings inside fences are data"
` + "```" + `
`
	out, errOut, err := runReleaseNotesExtractor(t, changelog, "v3.0.0-beta.6")
	require.NoError(t, err, errOut)
	require.Equal(t, want, out)

	lastOut, lastErrOut, lastErr := runReleaseNotesExtractor(t, changelog, "v3.0.0-beta.5")
	require.NoError(t, lastErr, lastErrOut)
	require.Equal(t, "### 🔧 Improvements / 改进\n\n- Earlier release.\n", lastOut)
}

func TestReleaseNotesWorkflowValidatesCurrentChangelog(t *testing.T) {
	root := repoRoot(t)
	command := exec.CommandContext(t.Context(), "awk", "-f", filepath.Join(root, "scripts", "extract-release-notes.awk"), filepath.Join(root, "CHANGELOG.md"))
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
}

func TestReleaseNotesWorkflowRejectsInvalidChangelog(t *testing.T) {
	tests := map[string]string{
		"missing target": `# Changelog

## [Unreleased]
`,
		"duplicate target": `# Changelog

## [Unreleased]

## [v3.0.0-beta.5] - 2026-09-01

### 🐛 Bug Fixes / 问题修复
- First.

## [v3.0.0-beta.5] - 2026-09-01

### 🐛 Bug Fixes / 问题修复
- Duplicate.
`,
		"duplicate unreleased": `# Changelog

## [Unreleased]

## [Unreleased]
`,
		"empty release": `# Changelog

## [Unreleased]

## [v3.0.0-beta.5] - 2026-09-01
`,
		"unsupported category": `# Changelog

## [Unreleased]

## [v3.0.0-beta.5] - 2026-09-01

### Internal refactor
- Hidden detail.
`,
		"empty category": `# Changelog

## [Unreleased]

## [v3.0.0-beta.5] - 2026-09-01

### 🐛 Bug Fixes / 问题修复
`,
		"invalid semver": `# Changelog

## [Unreleased]

## [v3.0.0-beta.05] - 2026-09-01

### 🐛 Bug Fixes / 问题修复
- Invalid version.
`,
		"entry without category": `# Changelog

## [Unreleased]

## [v3.0.0-beta.5] - 2026-09-01

- Missing category.
`,
		"malformed heading": `# Changelog

## [Unreleased]

## v3.0.0-beta.5

### 🐛 Bug Fixes / 问题修复
- Invalid boundary.
`,
		"crlf": "# Changelog\r\n\r\n## [Unreleased]\r\n",
	}

	for name, changelog := range tests {
		t.Run(name, func(t *testing.T) {
			_, errOut, err := runReleaseNotesExtractor(t, changelog, "v3.0.0-beta.5")
			require.Error(t, err)
			require.Contains(t, errOut, "CHANGELOG.md:")
		})
	}
}

func TestReleaseNotesWorkflowRepositoryPolicy(t *testing.T) {
	root := repoRoot(t)
	agents := readFile(t, filepath.Join(root, "AGENTS.md"))
	template := readFile(t, filepath.Join(root, ".github", "pull_request_template.md"))

	require.Contains(t, agents, "User-visible changes MUST add")
	require.Contains(t, agents, "skip-changelog")
	require.Contains(t, template, "I added every user-visible change")
	require.Contains(t, template, "This pull request has no user-visible change")
	require.Contains(t, template, "skip-changelog")
}

func runReleaseNotesExtractor(t *testing.T, changelog, version string) (string, string, error) {
	t.Helper()
	root := repoRoot(t)
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	require.NoError(t, os.WriteFile(path, []byte(changelog), 0o644))

	command := exec.CommandContext(
		t.Context(),
		"awk",
		"-v", "version="+version,
		"-f", filepath.Join(root, "scripts", "extract-release-notes.awk"),
		path,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return strings.ReplaceAll(stdout.String(), "\r\n", "\n"), stderr.String(), err
}
