function metric_name(series, brace) {
  brace = index(series, "{")
  if (brace == 0) {
    return series
  }
  return substr(series, 1, brace - 1)
}

function metric_labels(series, brace, last) {
  brace = index(series, "{")
  if (brace == 0) {
    return ""
  }
  last = length(series)
  if (substr(series, last, 1) != "}") {
    return ""
  }
  return substr(series, brace + 1, last - brace - 1)
}

function label_value(labels, key, marker, start, rest, last) {
  marker = key "=\""
  start = index(labels, marker)
  if (start == 0) {
    return ""
  }
  rest = substr(labels, start + length(marker))
  last = index(rest, "\"")
  if (last == 0) {
    return ""
  }
  return substr(rest, 1, last - 1)
}

function add_counter(key, phase, value) {
  if (phase == "before") {
    before[key] += value
  } else {
    after[key] += value
  }
}

function counter_delta(key, value) {
  value = after[key] - before[key]
  if (value < 0) {
    value = 0
  }
  return value
}

function max_value(current, value) {
  if (value > current) {
    return value
  }
  return current
}

function avg_delta_ms(sum_key, count_key, count) {
  count = counter_delta(count_key)
  if (count <= 0) {
    return 0
  }
  return counter_delta(sum_key) * 1000 / count
}

function avg_delta(sum_key, count_key, count) {
  count = counter_delta(count_key)
  if (count <= 0) {
    return 0
  }
  return counter_delta(sum_key) / count
}

BEGIN {
  if (duration == "") {
    duration = 0
  }
  duration += 0
  if (tag == "") {
    tag = "unknown"
  }
  if (node == "") {
    node = "unknown"
  }
}

/^[[:space:]]*#/ || NF < 2 {
  next
}

{
  series = $1
  value = $2 + 0
  phase = "after"
  if (FILENAME == ARGV[1]) {
    phase = "before"
  }
  name = metric_name(series)
  labels = metric_labels(series)

  if (phase == "after") {
    if (name == "wukongim_channelv2_active_runtimes") {
      role = label_value(labels, "role")
      active_total += value
      if (role == "leader") {
        active_leader += value
      } else if (role == "follower") {
        active_follower += value
      }
    } else if (name == "wukongim_channelv2_follower_parked") {
      follower_parked += value
    } else if (name == "wukongim_channelv2_reactor_mailbox_depth") {
      mailbox_depth_max = max_value(mailbox_depth_max, value)
    } else if (name == "wukongim_channelv2_worker_queue_depth") {
      worker_queue_depth_max = max_value(worker_queue_depth_max, value)
    }
  }

  if (name == "wukongim_channelv2_activation_rejected_total") {
    add_counter("activation_rejected", phase, value)
  } else if (name == "wukongim_channelv2_recovery_probe_total") {
    add_counter("recovery:" label_value(labels, "result"), phase, value)
  } else if (name == "wukongim_channelv2_pull_total") {
    add_counter("pull:" label_value(labels, "result") ":" label_value(labels, "empty"), phase, value)
  } else if (name == "wukongim_channelv2_rpc_pull_total") {
    add_counter("rpc_pull:" label_value(labels, "result"), phase, value)
  } else if (name == "wukongim_channelv2_meta_cache_total") {
    add_counter("meta_cache:" label_value(labels, "result"), phase, value)
  } else if (name == "wukongim_channelv2_append_duration_seconds_count") {
    add_counter("append_duration_count", phase, value)
  } else if (name == "wukongim_channelv2_append_duration_seconds_sum") {
    add_counter("append_duration_sum", phase, value)
  } else if (name == "wukongim_channelv2_append_batch_records_count") {
    add_counter("append_batch_count", phase, value)
  } else if (name == "wukongim_channelv2_append_batch_records_sum") {
    add_counter("append_batch_records_sum", phase, value)
  } else if (name == "wukongim_channelv2_append_batch_bytes_sum") {
    add_counter("append_batch_bytes_sum", phase, value)
  } else if (name == "wukongim_channelv2_append_batch_wait_duration_seconds_sum") {
    add_counter("append_batch_wait_sum", phase, value)
  } else if (name == "wukongim_channelv2_worker_task_duration_seconds_count") {
    add_counter("worker_task_count", phase, value)
  } else if (name == "wukongim_channelv2_worker_task_duration_seconds_sum") {
    add_counter("worker_task_sum", phase, value)
  }
}

END {
  rpc_pull_ok = counter_delta("rpc_pull:ok")
  rpc_pull_err = counter_delta("rpc_pull:err")
  rpc_pull_qps = 0
  if (duration > 0) {
    rpc_pull_qps = (rpc_pull_ok + rpc_pull_err) / duration
  }

  printf "%s\t%s\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.0f\t%.0f\t%.3f\t%.0f\t%.3f\t%.3f\t%.3f\t%.0f\t%.3f\n",
    tag,
    node,
    active_total,
    active_leader,
    active_follower,
    follower_parked,
    mailbox_depth_max,
    worker_queue_depth_max,
    counter_delta("activation_rejected"),
    counter_delta("recovery:submitted"),
    counter_delta("recovery:ok"),
    counter_delta("recovery:err"),
    counter_delta("pull:ok:false"),
    counter_delta("pull:ok:true"),
    counter_delta("pull:err:false") + counter_delta("pull:err:true"),
    rpc_pull_ok,
    rpc_pull_err,
    rpc_pull_qps,
    counter_delta("meta_cache:hit"),
    counter_delta("meta_cache:miss"),
    counter_delta("meta_cache:invalidate"),
    counter_delta("append_duration_count"),
    avg_delta_ms("append_duration_sum", "append_duration_count"),
    counter_delta("append_batch_count"),
    avg_delta("append_batch_records_sum", "append_batch_count"),
    avg_delta("append_batch_bytes_sum", "append_batch_count"),
    avg_delta_ms("append_batch_wait_sum", "append_batch_count"),
    counter_delta("worker_task_count"),
    avg_delta_ms("worker_task_sum", "worker_task_count")
}
