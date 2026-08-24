#!/usr/bin/awk -f

function has_label(line, name, value) {
  return index(line, name "=\"" value "\"") > 0
}

function observe(key, amount) {
  values[key] += amount
  seen[key] = 1
}

/^[[:space:]]*#/ || NF < 2 { next }

{
  metric = $1
  sub(/\{.*/, "", metric)
  if (!has_label($0, "store", "message")) {
    next
  }
  amount = $NF + 0
  if (metric == "wukongim_storage_commit_batch_requests_count") {
    observe("requests", amount)
  } else if (metric == "wukongim_storage_commit_batch_requests_bucket" && has_label($0, "le", "+Inf")) {
    observe("requests_histogram", amount)
  } else if (metric == "wukongim_storage_commit_batch_records_bucket" && has_label($0, "le", "+Inf")) {
    observe("records_histogram", amount)
  } else if (metric == "wukongim_storage_commit_batch_bytes_bucket" && has_label($0, "le", "+Inf")) {
    observe("bytes_histogram", amount)
  } else if (metric == "wukongim_storage_commit_batch_duration_seconds_count" && has_label($0, "result", "ok")) {
    for (i = 1; i <= 5; i++) {
      stage = i == 1 ? "collect" : i == 2 ? "build" : i == 3 ? "commit" : i == 4 ? "publish" : "total"
      if (has_label($0, "stage", stage)) {
        observe("stage_" stage, amount)
        break
      }
    }
  }
}

END {
  reference = values["requests"]
  required[1] = "requests"
  required[2] = "requests_histogram"
  required[3] = "records_histogram"
  required[4] = "bytes_histogram"
  required[5] = "stage_collect"
  required[6] = "stage_build"
  required[7] = "stage_commit"
  required[8] = "stage_publish"
  required[9] = "stage_total"
  for (i = 1; i <= 9; i++) {
    key = required[i]
    if (!(key in seen) || values[key] != reference) {
      exit 1
    }
  }
}
