#!/usr/bin/env bash
set -euo pipefail

# Input must be newest-first TSV rows: <artifact-id><tab><artifact-name>.
# Emit at most one artifact ID for each account/region binding plus one legacy
# provider-config artifact for runs created before binding-aware names existed.
awk -F '\t' '
  NF == 2 && $1 ~ /^[0-9]+$/ {
    name = $2
    binding = ""
    if (index(name, "cloud-sim-provider-config--") == 1) {
      count = split(name, parts, "--")
      region = parts[count - 1]
      account = parts[count]
      if (count >= 4 && region ~ /^[a-z0-9-]+$/ && account ~ /^[0-9a-f]+$/ && length(account) == 64) {
        binding = region "--" account
      }
    } else if (index(name, "cloud-sim-provider-config-") == 1) {
      run_id = substr(name, length("cloud-sim-provider-config-") + 1)
      if (run_id ~ /^[A-Za-z0-9][A-Za-z0-9._-]*$/ && length(run_id) <= 128) {
        binding = "legacy"
      }
    }
    if (binding != "" && !seen[binding]++) {
      print $1
    }
  }
'
