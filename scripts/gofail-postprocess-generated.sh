#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 2 ]]; then
  echo "usage: scripts/gofail-postprocess-generated.sh WORK_DIR PACKAGE:NAME..." >&2
  exit 2
fi

work_dir="$1"
shift

for spec in "$@"; do
  package_dir="${spec%%:*}"
  package_name="${spec#*:}"
  if [[ -z "$package_dir" || -z "$package_name" || "$package_dir" == "$spec" ]]; then
    echo "invalid package spec: $spec" >&2
    exit 2
  fi
  target_dir="$work_dir/$package_dir"
  if [[ ! -d "$target_dir" ]]; then
    echo "failpoint package directory not found: $target_dir" >&2
    exit 2
  fi
  while IFS= read -r -d '' file; do
    tmp_file="$file.tmp.$$"
    awk -v package_name="$package_name" '
      !rewritten && $1 == "package" {
        print "package " package_name
        rewritten = 1
        next
      }
      { print }
    ' "$file" > "$tmp_file"
    mv "$tmp_file" "$file"
  done < <(find "$target_dir" -maxdepth 1 -name '*.fail.go' -type f -print0)
done
