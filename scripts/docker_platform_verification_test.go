package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// Exercise the actual publication shell with a classic Docker store that
// rejects pulling an index digest under two different platform identities.
func TestDockerCanonicalProbeSelectsUniquePlatformManifests(t *testing.T) {
	var workflow struct {
		Jobs map[string]struct{ Steps []struct{ Name, Run string } }
	}
	require.NoError(t, yaml.Unmarshal(readWorkflow(t, "binary-release-publish.yml"), &workflow))
	var script string
	for _, step := range workflow.Jobs["docker"].Steps {
		if step.Name == "Verify canonical embedded Prometheus" {
			script = step.Run
		}
	}
	require.NotEmpty(t, script)
	amd := "sha256:" + strings.Repeat("a", 64)
	arm := "sha256:" + strings.Repeat("b", 64)
	index := "sha256:" + strings.Repeat("c", 64)
	for _, tc := range []struct {
		name, entries string
		ok            bool
	}{
		{"both platforms", `{"digest":"` + amd + `","platform":{"os":"linux","architecture":"amd64"}},{"digest":"` + arm + `","platform":{"os":"linux","architecture":"arm64"}}`, true},
		{"missing arm64", `{"digest":"` + amd + `","platform":{"os":"linux","architecture":"amd64"}}`, false},
		{"ambiguous amd64", `{"digest":"` + amd + `","platform":{"os":"linux","architecture":"amd64"}},{"digest":"` + arm + `","platform":{"os":"linux","architecture":"amd64"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			require.NoError(t, os.Mkdir(bin, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "canonical-manifest.json"), []byte(`{"manifest":{"manifests":[`+tc.entries+`]}}`), 0644))
			stubs := map[string]string{
				"docker": "#!/bin/sh\ncase \"$*\" in *\"$CANONICAL_DIGEST\"*) echo 'cannot overwrite index digest' >&2; exit 1;; esac\nprintf 'docker %s\\n' \"$*\" >>\"$CALLS\"\n",
				"bash":   "#!/bin/sh\nprintf 'probe %s\\n' \"$*\" >>\"$CALLS\"\n",
				"trivy":  "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do if [ \"$1\" = --output ]; then printf '{\"Results\":[{\"Type\":\"gobinary\"}]}' >\"$2\"; exit; fi; shift; done\nexit 1\n",
			}
			for name, body := range stubs {
				require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(body), 0755))
			}
			cmd := exec.Command("bash", "-c", strings.ReplaceAll(script, "${{ runner.temp }}", dir))
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "CANONICAL_IMAGE=registry.example/wukongim", "CANONICAL_DIGEST="+index, "WK_DOCKER_PROMETHEUS_ARTIFACT_DIR="+dir, "CALLS="+filepath.Join(dir, "calls"))
			out, err := cmd.CombinedOutput()
			if !tc.ok {
				require.Error(t, err, string(out))
				return
			}
			require.NoError(t, err, string(out))
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			require.NoError(t, err)
			require.Equal(t, "docker pull --platform linux/amd64 registry.example/wukongim@"+amd+"\nprobe scripts/verify-docker-prometheus.sh registry.example/wukongim@"+amd+" linux/amd64\ndocker pull --platform linux/arm64 registry.example/wukongim@"+arm+"\nprobe scripts/verify-docker-prometheus.sh registry.example/wukongim@"+arm+" linux/arm64\n", string(calls))
		})
	}
}
