#!/usr/bin/awk -f

function metric_name(token, brace) {
  brace = index(token, "{")
  if (brace > 0) return substr(token, 1, brace - 1)
  return token
}

/^#/ || NF < 2 { next }

{
  name = metric_name($1)
  if (name != "wukongim_runtime_pool_queue_depth" && name != "wukongim_runtime_pool_inflight") next
  value = $NF
  if (value !~ /^[0-9]+([.][0-9]+)?$/) {
    invalid = 1
    next
  }
  if (name == "wukongim_runtime_pool_queue_depth") {
    queue += value
    queue_samples++
  } else {
    inflight += value
    inflight_samples++
  }
}

END {
  if (invalid || queue_samples == 0 || inflight_samples == 0) {
    print "missing\t0\t0"
    exit 1
  }
  printf "complete\t%.0f\t%.0f\n", queue, inflight
}
