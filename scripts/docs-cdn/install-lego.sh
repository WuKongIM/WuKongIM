#!/usr/bin/env bash

set -euo pipefail
umask 077

readonly lego_module="github.com/go-acme/lego/v4"
readonly lego_version="v4.35.2"
readonly go_toolchain="go1.25.11"
readonly lego_sum="h1:uVQg+KC/yj9R2g7Q9W5wDqhvQvxV5SMu5eqFVoN5xZU="
readonly lego_go_mod_sum="h1:pX2jN5n8OphMGY1IaMjYm5DAEzguBaKRt8AvJAgJXpc="

fail() {
  printf 'install lego: %s\n' "$1" >&2
  exit 1
}

[[ $# -eq 1 ]] || fail "usage: $0 ABSOLUTE_INSTALL_DIRECTORY"
readonly install_directory="$1"
[[ "$install_directory" == /* ]] || fail "install directory must be absolute"
[[ "$install_directory" != / ]] || fail "refusing to install into the filesystem root"

for command in go jq; do
  command -v "$command" >/dev/null 2>&1 || fail "$command is required"
done

readonly bin_directory="${install_directory}/bin"
readonly module_cache="${install_directory}/modcache"
readonly build_cache="${install_directory}/buildcache"
readonly build_temporary_directory="${install_directory}/tmp"
[[ ! -e "${bin_directory}/lego" ]] || fail "target lego executable already exists"
mkdir -p -- "$bin_directory" "$module_cache" "$build_cache" "$build_temporary_directory"

export GOBIN="$bin_directory"
export GOCACHE="$build_cache"
export GOMODCACHE="$module_cache"
export GOTMPDIR="$build_temporary_directory"
export GOENV="off"
export GOTELEMETRY="off"
export GONOPROXY=""
export GONOSUMDB=""
export GOPRIVATE=""
export GOPROXY="https://proxy.golang.org,direct"
export GOSUMDB="sum.golang.org"
export GOTOOLCHAIN="$go_toolchain"
export GOWORK="off"

module_metadata="$(go mod download -json "${lego_module}@${lego_version}")" || \
  fail "could not download the pinned lego module"
jq -e \
  --arg module "$lego_module" \
  --arg version "$lego_version" \
  --arg sum "$lego_sum" \
  --arg go_mod_sum "$lego_go_mod_sum" \
  '.Path == $module and .Version == $version and .Sum == $sum and .GoModSum == $go_mod_sum and (.Error == null)' \
  <<<"$module_metadata" >/dev/null || fail "pinned lego module integrity check failed"

go install "${lego_module}/cmd/lego@${lego_version}"
readonly target_goos="$(go env GOOS)"
readonly target_goarch="$(go env GOARCH)"
readonly expected_version_output="lego version ${lego_version}+dev-release ${target_goos}/${target_goarch}"
readonly version_output="$("${bin_directory}/lego" --version 2>&1)"
[[ "$version_output" == "$expected_version_output" ]] || \
  fail "installed lego executable reported an unexpected version"

printf '%s\n' "${bin_directory}/lego"
