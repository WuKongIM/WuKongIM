#!/usr/bin/env bash
set -euo pipefail

# Input must be newest-first TSV rows: <artifact-id><tab><artifact-name>.
# Emit at most one artifact ID for each account/region binding. Pre-feature
# workflows stored their single provider config in a repository Variable and
# never produced provider-config artifacts, so unbound names are rejected.
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
    }
    if (binding != "" && !seen[binding]++) {
      print $1
    }
  }
'
