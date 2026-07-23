BEGIN {
  in_objectives = 0
  found = 0
}

/^objectives:[[:space:]]*(#.*)?$/ {
  in_objectives = 1
  next
}

in_objectives && /^[^[:space:]]/ {
  exit 1
}

in_objectives && /^[[:space:]]+scale:[[:space:]]*/ {
  value = $0
  sub(/^[[:space:]]+scale:[[:space:]]*/, "", value)
  sub(/[[:space:]]*(#.*)?$/, "", value)
  gsub(/^["']|["']$/, "", value)
  if (value !~ /^(small|medium|large)$/) {
    exit 1
  }
  print value
  found = 1
  exit 0
}

END {
  if (!found) {
    exit 1
  }
}
