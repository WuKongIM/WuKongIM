//go:build integration

package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDocsPagesOriginVerifierRejectsCertificateReady404UntilContentDeploys(t *testing.T) {
	root := repoRoot(t)
	fakeBin := t.TempDir()
	deployMarker := filepath.Join(t.TempDir(), "deployed")
	installDocsPagesOriginVerifierFakes(t, fakeBin)

	command := exec.Command("bash", filepath.Join(root, "scripts", "docs-cdn", "verify-pages-origin.sh"))
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCS_PAGES_DOMAIN=origin-docs.githubim.com",
		"DOCS_PAGES_REPOSITORY=WuKongIM/WuKongIM",
		"DOCS_PAGES_CACHE_BUST=test-run-1",
		"DOCS_PAGES_VERIFY_ATTEMPTS=2",
		"DOCS_PAGES_VERIFY_RETRY_SECONDS=1",
		"DOCS_PAGES_TEST_DEPLOY_MARKER="+deployMarker,
	)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(t, string(output), "content_not_ready")
	require.Contains(t, string(output), "origin_ready")
	require.FileExists(t, deployMarker)
}

func TestDocsPagesOriginVerifierNeverAcceptsCertificateReadyPermanent404(t *testing.T) {
	root := repoRoot(t)
	fakeBin := t.TempDir()
	installDocsPagesOriginVerifierFakes(t, fakeBin)

	command := exec.Command("bash", filepath.Join(root, "scripts", "docs-cdn", "verify-pages-origin.sh"))
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"DOCS_PAGES_DOMAIN=origin-docs.githubim.com",
		"DOCS_PAGES_REPOSITORY=WuKongIM/WuKongIM",
		"DOCS_PAGES_CACHE_BUST=test-run-2",
		"DOCS_PAGES_VERIFY_ATTEMPTS=2",
		"DOCS_PAGES_VERIFY_RETRY_SECONDS=1",
		"DOCS_PAGES_TEST_PERMANENT_404=true",
		"DOCS_PAGES_TEST_DEPLOY_MARKER="+filepath.Join(t.TempDir(), "never-deployed"),
	)
	output, err := command.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "content_not_ready")
	require.Contains(t, string(output), "origin did not become ready")
	require.NotContains(t, string(output), "origin_ready")
}

func installDocsPagesOriginVerifierFakes(t *testing.T, fakeBin string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(fakeBin, "gh"), []byte(`#!/usr/bin/env bash
set -euo pipefail
cat <<'JSON'
{"cname":"origin-docs.githubim.com","build_type":"workflow","protected_domain_state":"verified","https_enforced":true,"https_certificate":{"state":"approved","domains":["origin-docs.githubim.com"]}}
JSON
`), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(fakeBin, "sleep"), []byte(`#!/usr/bin/env bash
set -euo pipefail
if [[ "${DOCS_PAGES_TEST_PERMANENT_404:-}" != true ]]; then
  : >"$DOCS_PAGES_TEST_DEPLOY_MARKER"
fi
`), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(fakeBin, "curl"), []byte(`#!/usr/bin/env bash
set -euo pipefail
output=""
url=""
while (($# > 0)); do
  case "$1" in
    --output)
      output="$2"
      shift 2
      ;;
    --write-out|--connect-to|--connect-timeout|--max-time|--proto)
      shift 2
      ;;
    --compressed|--fail-with-body|--silent|--show-error|--tlsv1.2)
      shift
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done
[[ -n "$output" && -n "$url" ]]
if [[ ! -e "$DOCS_PAGES_TEST_DEPLOY_MARKER" ]]; then
  printf '<html>GitHub Pages 404</html>' >"$output"
  printf '404\ttext/html\t29\n'
  exit 22
fi
case "$url" in
  *'/api/search?'*)
    printf '{"type":"i18n","data":{"zh":{},"en":{}}}' >"$output"
    printf '200\tapplication/octet-stream\t42\n'
    ;;
  *)
    printf '<!doctype html><html><body>WuKongIM</body></html>' >"$output"
    printf '200\ttext/html; charset=utf-8\t52\n'
    ;;
esac
`), 0o700))
}
