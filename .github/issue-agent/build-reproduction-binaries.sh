#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 4 ]]; then
  echo "usage: build-reproduction-binaries.sh <affected-source> <diagnosis-source> <affected-output> <diagnosis-output>" >&2
  exit 2
fi

affected_source="$1"
diagnosis_source="$2"
affected_output="$3"
diagnosis_output="$4"

(cd "$affected_source" && \
  GOWORK=off go build -trimpath -o "$affected_output" ./cmd/wukongim)
(cd "$diagnosis_source" && \
  GOWORK=off go build -trimpath -o "$diagnosis_output" ./cmd/wukongim)
