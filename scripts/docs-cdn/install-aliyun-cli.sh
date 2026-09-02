#!/usr/bin/env bash

set -euo pipefail
umask 077

readonly cli_version="3.4.11"
readonly archive_name="aliyun-cli-linux-${cli_version}-amd64.tgz"
readonly archive_sha256="a7e3df497db14c10d4d7587795e9fa7849b0c51dfce02908b9de5a41fe717d5c"
readonly archive_url="https://github.com/aliyun/aliyun-cli/releases/download/v${cli_version}/${archive_name}"

fail() {
  printf 'install aliyun CLI: %s\n' "$1" >&2
  exit 1
}

[[ $# -eq 1 ]] || fail "usage: $0 ABSOLUTE_INSTALL_DIRECTORY"
readonly install_directory="$1"
[[ "$install_directory" == /* ]] || fail "install directory must be absolute"
[[ "$install_directory" != / ]] || fail "refusing to install into the filesystem root"
[[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || fail "only Linux x86_64 is supported"

for command in curl install mktemp sha256sum tar; do
  command -v "$command" >/dev/null 2>&1 || fail "$command is required"
done

readonly temporary_directory="$(mktemp -d)"
trap 'rm -rf -- "$temporary_directory"' EXIT
readonly archive_path="${temporary_directory}/${archive_name}"
readonly extract_directory="${temporary_directory}/extract"
mkdir -p -- "$extract_directory" "$install_directory"

curl \
  --fail \
  --location \
  --silent \
  --show-error \
  --proto '=https' \
  --tlsv1.2 \
  --connect-timeout 15 \
  --max-time 180 \
  --retry 3 \
  --retry-all-errors \
  --output "$archive_path" \
  "$archive_url"

printf '%s  %s\n' "$archive_sha256" "$archive_path" | sha256sum --check --strict

mapfile -t archive_entries < <(tar -tzf "$archive_path")
[[ ${#archive_entries[@]} -eq 1 && "${archive_entries[0]}" == aliyun ]] || \
  fail "release archive must contain only the aliyun executable"

tar -xzf "$archive_path" -C "$extract_directory" -- aliyun
install -m 0755 "$extract_directory/aliyun" "$install_directory/aliyun"
printf '%s\n' "$install_directory/aliyun"
