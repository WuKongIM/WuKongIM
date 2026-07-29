#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 2 ]]; then
  echo "usage: build-reproduction-binary.sh <source-dir> <output>" >&2
  exit 2
fi

source_dir="$1"
output="$2"

if [[ -d "$source_dir/cmd/wukongim" ]]; then
  entrypoint="./cmd/wukongim"
elif [[ -f "$source_dir/main.go" ]]; then
  entrypoint="."
else
  echo "No supported WuKongIM entrypoint in $source_dir" >&2
  exit 1
fi

(cd "$source_dir" && GOWORK=off go build -trimpath -o "$output" "$entrypoint")
