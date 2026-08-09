package scripts_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentArtifactContractsHaveCodeOwners(t *testing.T) {
	raw := readFile(t, filepath.Join(repoRoot(t), ".github", "CODEOWNERS"))
	ownersByPath := make(map[string][]string)
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		ownersByPath[fields[0]] = fields[1:]
	}

	for _, path := range []string{
		"/.agents/skills/",
		"/.agents/skill-tests.json",
		"/scripts/skillcheck/",
	} {
		require.Equal(t, []string{"@tangtaoit"}, ownersByPath[path], path)
	}
}
