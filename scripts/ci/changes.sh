#!/usr/bin/env bash
# Classify the files changed between BASE and HEAD into CI areas and print
# key=value lines (append them to $GITHUB_OUTPUT in Actions).
#
# An empty or all-zero BASE (push of a new branch, manual dispatch, schedule)
# selects every area, so nothing is skipped when the diff is unknown.
set -euo pipefail
base="${1:-}"
head="${2:-HEAD}"

# Plain variables (not associative arrays) keep this runnable on macOS bash 3.2.
areas="go web demo docs docs_golden packaging"
for area in $areas; do eval "sel_$area=false"; done

if [[ -z "$base" || "$base" =~ ^0+$ ]] || ! git cat-file -e "$base^{commit}" 2>/dev/null; then
  for area in $areas; do eval "sel_$area=true"; done
else
  while IFS= read -r path; do
    case "$path" in
      *.go|go.mod|go.sum|cmd/*|internal/*|pkg/*|scripts/*|docker/*|test/*|configs/*|.github/workflows/ci.yml)
        sel_go=true ;;
    esac
    case "$path" in
      web/*|internal/access/manager/*|internal/usecase/management/*|internal/runtime/online/*|test/e2e/cluster/*)
        sel_web=true ;;
    esac
    case "$path" in
      demo/*|internal/access/api/demoui/*) sel_demo=true ;;
    esac
    # Documentation contracts compare the site with server routes and DTOs, so
    # every Go change also runs them (this gap broke main for six days).
    case "$path" in
      docs-site/*|CHANGELOG.md|README.md|README_CN.md) sel_docs=true ;;
    esac
    # The documentation golden path drives a real single-node cluster through
    # the published JavaScript Web quickstart.
    case "$path" in
      docs-site/examples/*|docs-site/contracts/*|docs-site/content/docs/api/*|docs-site/content/docs/sdk/*|\
      internal/access/api/*|internal/access/gateway/*|internal/usecase/message/*|internal/usecase/user/*|\
      internal/config/*|pkg/gateway/*|pkg/protocol/*|pkg/cluster/channels/*|\
      test/e2e/message/javascript_web_quickstart/*|test/e2e/suite/*)
        sel_docs_golden=true ;;
    esac
    case "$path" in
      packaging/*|.goreleaser*.yaml|scripts/*native-package*|scripts/*native_package*|cmd/wukongim/*|cmd/wkcli/*)
        sel_packaging=true ;;
    esac
  done < <(git diff --name-only "$base" "$head")
  [[ "$sel_go" == false ]] || sel_docs=true
fi

for area in $areas; do eval "printf '%s=%s\\n' \"$area\" \"\$sel_$area\""; done
