#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 2 ]]; then
  echo "usage: check-reproduction-compatibility.sh <affected-source> <diagnosis-source>" >&2
  exit 2
fi

affected_source="$1"
diagnosis_source="$2"
marker=".github/issue-agent/reproduction-contract"

for source_dir in "$affected_source" "$diagnosis_source"; do
  if [[ ! -f "$source_dir/$marker" ]]; then
    echo "$source_dir lacks the reviewed reproduction contract" >&2
    exit 1
  fi
  if [[ ! -d "$source_dir/cmd/wukongim" ]]; then
    echo "$source_dir lacks the contract-compatible cmd/wukongim entrypoint" >&2
    exit 1
  fi
done

if ! cmp -s "$affected_source/$marker" "$diagnosis_source/$marker"; then
  echo "affected and diagnosis sources have incompatible reproduction contracts" >&2
  exit 1
fi

contract="$(tr -d '\n' <"$affected_source/$marker")"
if [[ ! "$contract" =~ ^[a-z0-9][a-z0-9._-]{0,63}$ ]]; then
  echo "reproduction contract marker is invalid" >&2
  exit 1
fi

echo "Verified reproduction contract: $contract"
