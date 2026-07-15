#!/usr/bin/env bash
set -euo pipefail

run_id="${1:-}"
[[ "$run_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || {
  echo "invalid Simulation Run identity" >&2
  exit 1
}

# Input must be TSV rows: <artifact-id><tab><artifact-name>. Accept exactly one
# account/region-bound provider-config artifact for the requested run.
awk -F '\t' -v run_id="$run_id" '
  NF == 2 && $1 ~ /^[0-9]+$/ {
    name = $2
    prefix = "cloud-sim-provider-config--" run_id "--"
    matches = 0
    if (index(name, prefix) == 1) {
      suffix = substr(name, length(prefix) + 1)
      count = split(suffix, parts, "--")
      matches = count == 2 && parts[1] ~ /^[a-z0-9-]+$/ &&
        parts[2] ~ /^[0-9a-f]+$/ && length(parts[2]) == 64
    }
    if (matches) {
      selected[++selected_count] = $1
      selected_region[selected_count] = parts[1]
      selected_account_hash[selected_count] = parts[2]
    }
  }
  END {
    if (selected_count != 1) {
      print "No unique provider config exists for exact Simulation Run " run_id "." > "/dev/stderr"
      exit 1
    }
    printf "%s\t%s\tsha256:%s\n", selected[1], selected_region[1], selected_account_hash[1]
  }
'
