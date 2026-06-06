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

function default_label(value) {
  if (value == "") {
    return "none"
  }
  return value
}

function row_key(component, pool, queue, priority) {
  return component "\034" pool "\034" queue "\034" priority
}

function remember_row(key, component, pool, queue, priority) {
  seen[key] = 1
  components[key] = component
  pools[key] = pool
  queues[key] = queue
  priorities[key] = priority
}

function key_from_labels(labels, queue, priority, component, pool, key) {
  component = default_label(label_value(labels, "component"))
  pool = default_label(label_value(labels, "pool"))
  queue = default_label(label_value(labels, "queue"))
  priority = default_label(label_value(labels, "priority"))
  key = row_key(component, pool, queue, priority)
  remember_row(key, component, pool, queue, priority)
  return key
}

function pool_key_from_labels(labels, component, pool, key) {
  component = default_label(label_value(labels, "component"))
  pool = default_label(label_value(labels, "pool"))
  key = row_key(component, pool, "none", "none")
  remember_row(key, component, pool, "none", "none")
  return key
}

function add_counter(key, result, phase, value) {
  if (phase == "before") {
    before[key, result] += value
  } else if (phase == "after") {
    after[key, result] += value
  }
}

function counter_delta(key, result, value) {
  value = after[key, result] - before[key, result]
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

function add_reason(reason, item) {
  if (reason == "") {
    return item
  }
  return reason "," item
}

BEGIN {
  if (tag == "") {
    tag = "unknown"
  }
  if (node == "") {
    node = "unknown"
  }
  if (backlog_min_depth == "") {
    backlog_min_depth = 1
  }
  backlog_min_depth += 0
}

/^[[:space:]]*#/ || NF < 2 {
  next
}

{
  series = $1
  value = $2 + 0
  phase = "sample"
  if (FILENAME == ARGV[1]) {
    phase = "before"
  } else if (FILENAME == ARGV[2]) {
    phase = "after"
  }
  name = metric_name(series)
  labels = metric_labels(series)

  if (name == "wukongim_runtime_pool_admission_total") {
    key = key_from_labels(labels)
    add_counter(key, default_label(label_value(labels, "result")), phase, value)
    next
  }

  if (phase == "before") {
    next
  }

  if (name == "wukongim_runtime_pool_queue_depth") {
    key = key_from_labels(labels)
    queue_depth_max[key] = max_value(queue_depth_max[key], value)
  } else if (name == "wukongim_runtime_pool_queue_capacity") {
    key = key_from_labels(labels)
    queue_capacity[key] = max_value(queue_capacity[key], value)
  } else if (name == "wukongim_runtime_pool_queue_bytes") {
    key = key_from_labels(labels)
    queue_bytes_max[key] = max_value(queue_bytes_max[key], value)
  } else if (name == "wukongim_runtime_pool_queue_bytes_capacity") {
    key = key_from_labels(labels)
    queue_bytes_capacity[key] = max_value(queue_bytes_capacity[key], value)
  } else if (name == "wukongim_runtime_pool_inflight") {
    key = pool_key_from_labels(labels)
    inflight_max[key] = max_value(inflight_max[key], value)
  } else if (name == "wukongim_runtime_pool_workers") {
    key = pool_key_from_labels(labels)
    workers[key] = max_value(workers[key], value)
  }
}

END {
  for (key in seen) {
    depth = queue_depth_max[key] + 0
    capacity = queue_capacity[key] + 0
    fill = 0
    if (capacity > 0) {
      fill = depth / capacity
    }
    queue_bytes = queue_bytes_max[key] + 0
    bytes_capacity = queue_bytes_capacity[key] + 0
    bytes_fill = 0
    if (bytes_capacity > 0) {
      bytes_fill = queue_bytes / bytes_capacity
    }
    inflight = inflight_max[key] + 0
    worker_count = workers[key] + 0
    inflight_util = 0
    if (worker_count > 0) {
      inflight_util = inflight / worker_count
    }
    full = counter_delta(key, "full")
    busy = counter_delta(key, "busy")
    dirty = counter_delta(key, "dirty")
    requeued = counter_delta(key, "requeued")

    reason = ""
    if (backlog_min_depth > 0 && depth >= backlog_min_depth) {
      reason = add_reason(reason, "queue_backlog")
    }
    if (capacity > 0 && depth >= capacity) {
      reason = add_reason(reason, "queue_full")
    }
    if (worker_count > 0 && inflight >= worker_count) {
      reason = add_reason(reason, "worker_saturated")
    }
    if (full > 0) {
      reason = add_reason(reason, "admission_full")
    }
    if (busy > 0) {
      reason = add_reason(reason, "admission_busy")
    }
    if (dirty > 0) {
      reason = add_reason(reason, "admission_dirty")
    }
    if (requeued > 0) {
      reason = add_reason(reason, "admission_requeued")
    }
    if (reason == "") {
      continue
    }

    printf "%s\t%s\t%s\t%s\t%s\t%s\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.0f\t%.0f\t%s\n",
      tag,
      node,
      components[key],
      pools[key],
      queues[key],
      priorities[key],
      depth,
      capacity,
      fill,
      queue_bytes,
      bytes_capacity,
      bytes_fill,
      inflight,
      worker_count,
      inflight_util,
      full,
      busy,
      dirty,
      requeued,
      reason
  }
}
