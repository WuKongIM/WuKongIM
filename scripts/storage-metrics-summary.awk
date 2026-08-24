BEGIN {
  OFS = "\t"
  if (header) {
    print "tag", "node", "evidence", "commit_queue_depth_max", "physical_commits_delta", \
      "logical_requests_delta", "records_delta", "bytes_delta", "avg_requests_per_commit", \
      "avg_records_per_commit", "collect_avg_ms", "build_avg_ms", "commit_avg_ms", \
      "publish_avg_ms", "total_avg_ms", "request_count_delta", "request_avg_ms", \
      "request_ok_delta", "request_ok_avg_ms", "request_timeout_delta", "request_timeout_avg_ms", \
      "request_canceled_delta", "request_canceled_avg_ms", "request_error_delta", "request_error_avg_ms", \
      "leader_append_request_delta", "leader_append_request_avg_ms", \
      "follower_apply_request_delta", "follower_apply_request_avg_ms", \
      "message_append_request_delta", "message_append_request_avg_ms", \
      "wal_bytes_in_delta", "wal_bytes_written_delta", "wal_write_amplification", \
      "flush_bytes_delta", "flush_count_delta", "compaction_bytes_read_delta", \
      "compaction_bytes_written_delta", "compaction_count_delta", "sstable_size_max", \
      "compaction_debt_max", "compactions_in_progress_max", "read_amplification_max", \
      "disk_usage_max", "avg_bytes_per_commit", \
      "requests_per_commit_p50", "requests_per_commit_p95", "requests_per_commit_p99", \
      "records_per_commit_p50", "records_per_commit_p95", "records_per_commit_p99", \
      "bytes_per_commit_p50", "bytes_per_commit_p95", "bytes_per_commit_p99"
    exit
  }
  evidence = "complete"
}

function has_label(line, name, value) {
  return index(line, name "=\"" value "\"") > 0
}

function label_value(line, name, marker, start, rest, finish) {
  marker = "{" name "=\""
  start = index(line, marker)
  if (start == 0) {
    marker = "," name "=\""
    start = index(line, marker)
    if (start == 0) {
      return ""
    }
  }
  rest = substr(line, start + length(marker))
  finish = index(rest, "\"")
  if (finish == 0) {
    return ""
  }
  return substr(rest, 1, finish - 1)
}

function add_value(key, amount, compound) {
  compound = file_index SUBSEP key
  values[compound] += amount
  seen[compound] = 1
}

function add_histogram_value(key, bound, amount, compound) {
  compound = file_index SUBSEP key SUBSEP bound
  histogram_values[compound] += amount
  histogram_seen[compound] = 1
  histogram_family_seen[file_index SUBSEP key] = 1
}

function observe_max(key, amount) {
  if (!(key in max_seen) || amount > maxima[key]) {
    maxima[key] = amount
  }
  max_seen[key] = 1
}

function mark_evidence(reason) {
  if (reason == "missing" || evidence == "complete") {
    evidence = reason
  }
}

function delta(key, first_key, last_key, first_value, last_value) {
  first_key = 1 SUBSEP key
  last_key = file_index SUBSEP key
  if (!(first_key in seen) || !(last_key in seen)) {
    mark_evidence("missing")
    return 0
  }
  first_value = values[first_key]
  last_value = values[last_key]
  if (last_value < first_value) {
    mark_evidence("counter_reset")
    return 0
  }
  return last_value - first_value
}

function optional_delta(key, first_key, last_key, first_value, last_value) {
  first_key = 1 SUBSEP key
  last_key = file_index SUBSEP key
  first_value = first_key in seen ? values[first_key] : 0
  last_value = last_key in seen ? values[last_key] : 0
  if (last_value < first_value) {
    mark_evidence("counter_reset")
    return 0
  }
  return last_value - first_value
}

function lazy_delta(key, first_key, last_key, first_value, last_value) {
  first_key = 1 SUBSEP key
  last_key = file_index SUBSEP key
  if (!(last_key in seen)) {
    mark_evidence("missing")
    return 0
  }
  first_value = first_key in seen ? values[first_key] : 0
  last_value = values[last_key]
  if (last_value < first_value) {
    mark_evidence("counter_reset")
    return 0
  }
  return last_value - first_value
}

function required_max(key) {
  if (!(key in max_seen)) {
    mark_evidence("missing")
    return 0
  }
  return maxima[key]
}

function ratio(numerator, denominator) {
  if (denominator <= 0) {
    return 0
  }
  return numerator / denominator
}

function absolute(value) {
  return value < 0 ? -value : value
}

# histogram_quantile applies Prometheus' linear interpolation to the delta of
# one cumulative histogram. A histogram may appear lazily after the first
# snapshot, but a partially missing boundary or a non-monotonic delta fails the
# evidence closed.
function histogram_quantile(key, quantile, bounds, parts, compound, bound_count, bound, i, j, swap, \
                            first_family, last_family, first_value, last_value, cumulative, total, \
                            rank, previous_count, previous_bound, bucket_count, fraction) {
  first_family = 1 SUBSEP key
  last_family = file_index SUBSEP key
  histogram_total_value = 0
  histogram_valid_value = 0
  if (!(last_family in histogram_family_seen)) {
    mark_evidence("missing")
    return 0
  }

  bound_count = 0
  for (compound in histogram_seen) {
    split(compound, parts, SUBSEP)
    if ((parts[1] + 0) == file_index && parts[2] == key) {
      bounds[++bound_count] = parts[3]
    }
  }
  if (bound_count == 0) {
    mark_evidence("missing")
    return 0
  }
  for (i = 2; i <= bound_count; i++) {
    swap = bounds[i]
    j = i - 1
    while (j >= 1 && (bounds[j] == "+Inf" || (swap != "+Inf" && bounds[j] + 0 > swap + 0))) {
      bounds[j + 1] = bounds[j]
      j--
    }
    bounds[j + 1] = swap
  }
  if (bounds[bound_count] != "+Inf") {
    mark_evidence("missing")
    return 0
  }

  if (first_family in histogram_family_seen) {
    for (compound in histogram_seen) {
      split(compound, parts, SUBSEP)
      if ((parts[1] + 0) == 1 && parts[2] == key &&
          !((file_index SUBSEP key SUBSEP parts[3]) in histogram_seen)) {
        mark_evidence("missing")
        return 0
      }
    }
  }

  previous_count = 0
  for (i = 1; i <= bound_count; i++) {
    bound = bounds[i]
    compound = file_index SUBSEP key SUBSEP bound
    first_value = 0
    if (first_family in histogram_family_seen) {
      if (!((1 SUBSEP key SUBSEP bound) in histogram_seen)) {
        mark_evidence("missing")
        return 0
      }
      first_value = histogram_values[1 SUBSEP key SUBSEP bound]
    }
    last_value = histogram_values[compound]
    if (last_value < first_value) {
      mark_evidence("counter_reset")
      return 0
    }
    cumulative = last_value - first_value
    if (cumulative < previous_count) {
      mark_evidence("counter_reset")
      return 0
    }
    histogram_deltas[i] = cumulative
    previous_count = cumulative
  }

  total = histogram_deltas[bound_count]
  histogram_total_value = total
  histogram_valid_value = 1
  if (total <= 0) {
    return 0
  }
  rank = quantile * total
  previous_count = 0
  previous_bound = 0
  for (i = 1; i <= bound_count; i++) {
    cumulative = histogram_deltas[i]
    bound = bounds[i]
    if (cumulative < rank) {
      previous_count = cumulative
      if (bound != "+Inf") {
        previous_bound = bound + 0
      }
      continue
    }
    if (bound == "+Inf") {
      return previous_bound
    }
    bucket_count = cumulative - previous_count
    if (bucket_count <= 0) {
      return bound + 0
    }
    fraction = (rank - previous_count) / bucket_count
    return previous_bound + ((bound + 0) - previous_bound) * fraction
  }
  return previous_bound
}

FNR == 1 {
  file_index++
}

/^[[:space:]]*#/ || NF < 2 {
  next
}

{
  metric = $1
  sub(/\{.*/, "", metric)
  amount = $NF + 0
  if (metric ~ /^wukongim_storage_commit_/ && !has_label($0, "store", "message")) {
    next
  }
  if (metric ~ /^wukongim_storage_pebble_/ && !has_label($0, "store", "channel_log")) {
    next
  }

  if (metric == "wukongim_storage_commit_queue_depth") {
    observe_max("commit_queue_depth", amount)
  } else if (metric == "wukongim_storage_commit_batch_requests_count") {
    add_value("batch_count", amount)
  } else if (metric == "wukongim_storage_commit_batch_requests_sum") {
    add_value("batch_requests_sum", amount)
  } else if (metric == "wukongim_storage_commit_batch_requests_bucket") {
    bound = label_value($0, "le")
    if (bound == "") {
      mark_evidence("missing")
    } else {
      add_histogram_value("batch_requests", bound, amount)
    }
  } else if (metric == "wukongim_storage_commit_batch_records_sum") {
    add_value("batch_records_sum", amount)
  } else if (metric == "wukongim_storage_commit_batch_records_bucket") {
    bound = label_value($0, "le")
    if (bound == "") {
      mark_evidence("missing")
    } else {
      add_histogram_value("batch_records", bound, amount)
    }
  } else if (metric == "wukongim_storage_commit_batch_bytes_sum") {
    add_value("batch_bytes_sum", amount)
  } else if (metric == "wukongim_storage_commit_batch_bytes_bucket") {
    bound = label_value($0, "le")
    if (bound == "") {
      mark_evidence("missing")
    } else {
      add_histogram_value("batch_bytes", bound, amount)
    }
  } else if (metric == "wukongim_storage_commit_batch_duration_seconds_count" ||
             metric == "wukongim_storage_commit_batch_duration_seconds_sum") {
    suffix = metric ~ /_count$/ ? "count" : "sum"
    if (has_label($0, "result", "ok")) {
      for (stage_index = 1; stage_index <= 5; stage_index++) {
        stage = stage_index == 1 ? "collect" : stage_index == 2 ? "build" : \
          stage_index == 3 ? "commit" : stage_index == 4 ? "publish" : "total"
        if (has_label($0, "stage", stage)) {
          add_value(stage "_" suffix, amount)
          break
        }
      }
    }
  } else if (metric == "wukongim_storage_commit_request_duration_seconds_count" ||
             metric == "wukongim_storage_commit_request_duration_seconds_sum") {
    suffix = metric ~ /_count$/ ? "count" : "sum"
    add_value("request_" suffix, amount)
    if (has_label($0, "result", "ok")) {
      add_value("request_ok_" suffix, amount)
    } else if (has_label($0, "result", "timeout")) {
      add_value("request_timeout_" suffix, amount)
    } else if (has_label($0, "result", "canceled")) {
      add_value("request_canceled_" suffix, amount)
    } else {
      add_value("request_error_" suffix, amount)
    }
    if (has_label($0, "lane", "leader_append")) {
      add_value("leader_append_" suffix, amount)
    } else if (has_label($0, "lane", "follower_apply") ||
               has_label($0, "lane", "replica_foreground") ||
               has_label($0, "lane", "replica_trailing")) {
      add_value("follower_apply_" suffix, amount)
    } else if (has_label($0, "lane", "message_append")) {
      add_value("message_append_" suffix, amount)
    }
  } else if (metric == "wukongim_storage_pebble_wal_bytes_in") {
    add_value("wal_bytes_in", amount)
  } else if (metric == "wukongim_storage_pebble_wal_bytes_written") {
    add_value("wal_bytes_written", amount)
  } else if (metric == "wukongim_storage_pebble_flush_bytes_written") {
    add_value("flush_bytes", amount)
  } else if (metric == "wukongim_storage_pebble_flush_count") {
    add_value("flush_count", amount)
  } else if (metric == "wukongim_storage_pebble_compaction_bytes_read") {
    add_value("compaction_bytes_read", amount)
  } else if (metric == "wukongim_storage_pebble_compaction_bytes_written") {
    add_value("compaction_bytes_written", amount)
  } else if (metric == "wukongim_storage_pebble_compaction_count") {
    add_value("compaction_count", amount)
  } else if (metric == "wukongim_storage_pebble_sstable_size_bytes") {
    observe_max("sstable_size", amount)
  } else if (metric == "wukongim_storage_pebble_compaction_estimated_debt_bytes") {
    observe_max("compaction_debt", amount)
  } else if (metric == "wukongim_storage_pebble_compactions_in_progress") {
    observe_max("compactions_in_progress", amount)
  } else if (metric == "wukongim_storage_pebble_read_amplification") {
    observe_max("read_amplification", amount)
  } else if (metric == "wukongim_storage_pebble_disk_usage_bytes") {
    observe_max("disk_usage", amount)
  }
}

END {
  if (header) {
    exit
  }
  if (file_index < 2) {
    mark_evidence("missing")
  }

  queue_max = required_max("commit_queue_depth")
  commit_count = lazy_delta("batch_count")
  logical_requests = lazy_delta("batch_requests_sum")
  records = lazy_delta("batch_records_sum")
  bytes = lazy_delta("batch_bytes_sum")
  requests_p50 = histogram_quantile("batch_requests", 0.50)
  requests_histogram_count = histogram_total_value
  requests_histogram_valid = histogram_valid_value
  requests_p95 = histogram_quantile("batch_requests", 0.95)
  requests_p99 = histogram_quantile("batch_requests", 0.99)
  records_p50 = histogram_quantile("batch_records", 0.50)
  records_histogram_count = histogram_total_value
  records_histogram_valid = histogram_valid_value
  records_p95 = histogram_quantile("batch_records", 0.95)
  records_p99 = histogram_quantile("batch_records", 0.99)
  bytes_p50 = histogram_quantile("batch_bytes", 0.50)
  bytes_histogram_count = histogram_total_value
  bytes_histogram_valid = histogram_valid_value
  bytes_p95 = histogram_quantile("batch_bytes", 0.95)
  bytes_p99 = histogram_quantile("batch_bytes", 0.99)
  if ((requests_histogram_valid && absolute(requests_histogram_count - commit_count) > 0.000001) ||
      (records_histogram_valid && absolute(records_histogram_count - commit_count) > 0.000001) ||
      (bytes_histogram_valid && absolute(bytes_histogram_count - commit_count) > 0.000001)) {
    mark_evidence("missing")
  }
  collect_count = lazy_delta("collect_count")
  collect_sum = lazy_delta("collect_sum")
  build_count = lazy_delta("build_count")
  build_sum = lazy_delta("build_sum")
  physical_count = lazy_delta("commit_count")
  physical_sum = lazy_delta("commit_sum")
  publish_count = lazy_delta("publish_count")
  publish_sum = lazy_delta("publish_sum")
  total_count = lazy_delta("total_count")
  total_sum = lazy_delta("total_sum")
  request_count = lazy_delta("request_count")
  request_sum = lazy_delta("request_sum")
  request_ok = optional_delta("request_ok_count")
  request_ok_sum = optional_delta("request_ok_sum")
  request_timeout = optional_delta("request_timeout_count")
  request_timeout_sum = optional_delta("request_timeout_sum")
  request_canceled = optional_delta("request_canceled_count")
  request_canceled_sum = optional_delta("request_canceled_sum")
  request_error = optional_delta("request_error_count")
  request_error_sum = optional_delta("request_error_sum")
  leader_append_count = optional_delta("leader_append_count")
  leader_append_sum = optional_delta("leader_append_sum")
  follower_apply_count = optional_delta("follower_apply_count")
  follower_apply_sum = optional_delta("follower_apply_sum")
  message_append_count = optional_delta("message_append_count")
  message_append_sum = optional_delta("message_append_sum")
  if (request_ok + request_timeout + request_canceled + request_error != request_count ||
      leader_append_count + follower_apply_count + message_append_count != request_count) {
    mark_evidence("missing")
  }
  wal_in = delta("wal_bytes_in")
  wal_written = delta("wal_bytes_written")
  flush_bytes = delta("flush_bytes")
  flush_count = delta("flush_count")
  compaction_read = delta("compaction_bytes_read")
  compaction_written = delta("compaction_bytes_written")
  compaction_count = delta("compaction_count")
  sstable_max = required_max("sstable_size")
  debt_max = required_max("compaction_debt")
  compactions_max = required_max("compactions_in_progress")
  read_amplification_max = required_max("read_amplification")
  disk_usage_max = required_max("disk_usage")

  printf "%s\t%s\t%s\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f\t%.0f\t%.6f\t%.0f\t%.6f\t%.0f\t%.6f\t%.0f\t%.6f\t%.0f\t%.6f\t%.0f\t%.6f\t%.0f\t%.6f\t%.0f\t%.6f\t%.0f\t%.0f\t%.6f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f", \
    tag, node, evidence, queue_max, commit_count, logical_requests, records, bytes, \
    ratio(logical_requests, commit_count), ratio(records, commit_count), \
    ratio(collect_sum * 1000, collect_count), ratio(build_sum * 1000, build_count), \
    ratio(physical_sum * 1000, physical_count), ratio(publish_sum * 1000, publish_count), \
    ratio(total_sum * 1000, total_count), request_count, ratio(request_sum * 1000, request_count), \
    request_ok, ratio(request_ok_sum * 1000, request_ok), \
    request_timeout, ratio(request_timeout_sum * 1000, request_timeout), \
    request_canceled, ratio(request_canceled_sum * 1000, request_canceled), \
    request_error, ratio(request_error_sum * 1000, request_error), \
    leader_append_count, ratio(leader_append_sum * 1000, leader_append_count), \
    follower_apply_count, ratio(follower_apply_sum * 1000, follower_apply_count), \
    message_append_count, ratio(message_append_sum * 1000, message_append_count), \
    wal_in, wal_written, ratio(wal_written, wal_in), \
    flush_bytes, flush_count, compaction_read, compaction_written, compaction_count, sstable_max, debt_max, \
    compactions_max, read_amplification_max, disk_usage_max
  printf "\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f\t%.6f\n", \
    ratio(bytes, commit_count), requests_p50, requests_p95, requests_p99, \
    records_p50, records_p95, records_p99, bytes_p50, bytes_p95, bytes_p99
}
