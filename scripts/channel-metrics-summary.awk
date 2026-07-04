# Promoted Channel runtime summary entrypoint. The channelv2-named script is a
# compatibility symlink for pre-promotion callers.
function metric_name(series, brace) {
  brace = index(series, "{")
  if (brace == 0) {
    return series
  }
  return substr(series, 1, brace - 1)
}

function channel_runtime_metric_name(name) {
  if (substr(name, 1, length("wukongim_channel_")) == "wukongim_channel_") {
    return "wukongim_channelv2_" substr(name, length("wukongim_channel_") + 1)
  }
  return name
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

function runtime_component_label(component) {
  if (component == "channelv2") {
    return "channel"
  }
  if (component == "transportv2") {
    return "transport"
  }
  return component
}

function runtime_pool_key(labels) {
  return runtime_component_label(label_value(labels, "component")) "\034" label_value(labels, "pool")
}

function runtime_queue_key(labels) {
  return runtime_pool_key(labels) "\034" label_value(labels, "queue") "\034" label_value(labels, "priority")
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
  raw_name = metric_name(series)
  name = channel_runtime_metric_name(raw_name)
  labels = metric_labels(series)
  if (raw_name != name || substr(raw_name, 1, length("wukongim_channelv2_")) == "wukongim_channelv2_") {
    canonical_series = phase "\034" name "\034" labels
    if (seen_channel_runtime_series[canonical_series]++) {
      next
    }
  }

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
    } else if (name == "wukongim_runtime_pool_queue_depth") {
      key = runtime_queue_key(labels)
      runtime_queue_depth[key] = value
    } else if (name == "wukongim_runtime_pool_queue_capacity") {
      key = runtime_queue_key(labels)
      runtime_queue_capacity[key] = value
    } else if (name == "wukongim_runtime_pool_queue_bytes") {
      key = runtime_queue_key(labels)
      runtime_queue_bytes[key] = value
    } else if (name == "wukongim_runtime_pool_queue_bytes_capacity") {
      key = runtime_queue_key(labels)
      runtime_queue_bytes_capacity[key] = value
    } else if (name == "wukongim_runtime_pool_inflight") {
      key = runtime_pool_key(labels)
      runtime_pool_inflight[key] = value
    } else if (name == "wukongim_runtime_pool_workers") {
      key = runtime_pool_key(labels)
      runtime_pool_workers[key] = value
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
  } else if (name == "wukongim_channelv2_worker_batch_items_count") {
    add_counter("worker_batch_count:" label_value(labels, "kind"), phase, value)
  } else if (name == "wukongim_channelv2_worker_batch_items_sum") {
    add_counter("worker_batch_items:" label_value(labels, "kind"), phase, value)
  } else if (name == "wukongim_runtime_pool_admission_total") {
    add_counter("runtime_admission:" label_value(labels, "result"), phase, value)
  }
}

END {
  for (key in runtime_queue_depth) {
    depth = runtime_queue_depth[key]
    capacity = runtime_queue_capacity[key]
    runtime_pool_queue_depth_max = max_value(runtime_pool_queue_depth_max, depth)
    if (capacity > 0) {
      runtime_pool_queue_fill_max = max_value(runtime_pool_queue_fill_max, depth / capacity)
    }
  }
  for (key in runtime_queue_bytes) {
    bytes = runtime_queue_bytes[key]
    bytes_capacity = runtime_queue_bytes_capacity[key]
    runtime_pool_queue_bytes_max = max_value(runtime_pool_queue_bytes_max, bytes)
    if (bytes_capacity > 0) {
      runtime_pool_queue_bytes_fill_max = max_value(runtime_pool_queue_bytes_fill_max, bytes / bytes_capacity)
    }
  }
  for (key in runtime_pool_inflight) {
    inflight = runtime_pool_inflight[key]
    workers = runtime_pool_workers[key]
    runtime_pool_inflight_max = max_value(runtime_pool_inflight_max, inflight)
    if (workers > 0) {
      runtime_pool_inflight_util_max = max_value(runtime_pool_inflight_util_max, inflight / workers)
    }
  }

  rpc_pull_ok = counter_delta("rpc_pull:ok")
  rpc_pull_err = counter_delta("rpc_pull:err")
  rpc_pull_qps = 0
  if (duration > 0) {
    rpc_pull_qps = (rpc_pull_ok + rpc_pull_err) / duration
  }
  rpc_pull_batch_calls = counter_delta("worker_batch_count:rpc_pull")
  rpc_pull_batch_items = counter_delta("worker_batch_items:rpc_pull")
  rpc_pull_batch_avg = 0
  if (rpc_pull_batch_calls > 0) {
    rpc_pull_batch_avg = rpc_pull_batch_items / rpc_pull_batch_calls
  }
  rpc_pull_hint_batch_calls = counter_delta("worker_batch_count:rpc_pull_hint")
  rpc_pull_hint_batch_items = counter_delta("worker_batch_items:rpc_pull_hint")
  rpc_pull_hint_batch_avg = 0
  if (rpc_pull_hint_batch_calls > 0) {
    rpc_pull_hint_batch_avg = rpc_pull_hint_batch_items / rpc_pull_hint_batch_calls
  }
  store_append_batch_calls = counter_delta("worker_batch_count:store_append")
  store_append_batch_items = counter_delta("worker_batch_items:store_append")
  store_append_batch_avg = 0
  if (store_append_batch_calls > 0) {
    store_append_batch_avg = store_append_batch_items / store_append_batch_calls
  }
  store_apply_batch_calls = counter_delta("worker_batch_count:store_apply")
  store_apply_batch_items = counter_delta("worker_batch_items:store_apply")
  store_apply_batch_avg = 0
  if (store_apply_batch_calls > 0) {
    store_apply_batch_avg = store_apply_batch_items / store_apply_batch_calls
  }

  printf "%s\t%s\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.3f\t%.0f\t%.3f\t%.0f\t%.3f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.0f\t%.0f\t%.3f\t%.0f\t%.3f\t%.3f\t%.3f\t%.0f\t%.3f\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.3f\n",
    tag,
    node,
    active_total,
    active_leader,
    active_follower,
    follower_parked,
    mailbox_depth_max,
    worker_queue_depth_max,
    runtime_pool_queue_depth_max,
    runtime_pool_queue_fill_max,
    runtime_pool_queue_bytes_max,
    runtime_pool_queue_bytes_fill_max,
    runtime_pool_inflight_max,
    runtime_pool_inflight_util_max,
    counter_delta("runtime_admission:full"),
    counter_delta("runtime_admission:busy"),
    counter_delta("runtime_admission:dirty"),
    counter_delta("runtime_admission:requeued"),
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
    avg_delta_ms("worker_task_sum", "worker_task_count"),
    rpc_pull_batch_calls,
    rpc_pull_batch_items,
    rpc_pull_batch_avg,
    rpc_pull_hint_batch_calls,
    rpc_pull_hint_batch_items,
    rpc_pull_hint_batch_avg,
    store_append_batch_calls,
    store_append_batch_items,
    store_append_batch_avg,
    store_apply_batch_calls,
    store_apply_batch_items,
    store_apply_batch_avg
}
