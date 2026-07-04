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
    return "unknown"
  }
  return value
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

function pool_key(component, pool) {
  return component "\034" pool
}

function remember_pool(key, component, pool) {
  if (!(key in pool_seen)) {
    pool_seen[key] = 1
    pool_order[++pool_count] = key
    pool_component[key] = component
    pool_name[key] = pool
  }
}

function pool_key_from_labels(labels, component, pool, key) {
  component = runtime_component_label(default_label(label_value(labels, "component")))
  pool = default_label(label_value(labels, "pool"))
  key = pool_key(component, pool)
  remember_pool(key, component, pool)
  return key
}

function remember_current_pool(key) {
  if (!(key in current_seen)) {
    current_seen[key] = 1
    current_order[++current_count] = key
  }
}

function reset_current_file(i, key) {
  for (i = 1; i <= current_count; i++) {
    key = current_order[i]
    delete current_seen[key]
    delete current_order[i]
    delete current_running[key]
    delete current_capacity[key]
    delete current_waiting[key]
    delete current_utilization[key]
  }
  current_count = 0
}

function flush_current_file(i, key, util, computed) {
  for (i = 1; i <= current_count; i++) {
    key = current_order[i]
    util = current_utilization[key] + 0
    if (current_capacity[key] > 0) {
      computed = current_running[key] / current_capacity[key]
      if (computed > util) {
        util = computed
      }
    }
    if (!(key in peak_seen) || util > peak_utilization[key] || (util == peak_utilization[key] && current_running[key] > peak_running[key])) {
      peak_seen[key] = 1
      peak_running[key] = current_running[key] + 0
      peak_capacity[key] = current_capacity[key] + 0
      peak_waiting[key] = current_waiting[key] + 0
      peak_utilization[key] = util
    }
  }
  reset_current_file()
}

BEGIN {
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
  if (current_file != FILENAME) {
    if (current_file != "") {
      flush_current_file()
    }
    current_file = FILENAME
  }

  series = $1
  value = $2 + 0
  phase = "sample"
  if (FILENAME == ARGV[1]) {
    phase = "before"
  }
  if (phase == "before") {
    next
  }

  name = metric_name(series)
  labels = metric_labels(series)

  if (name == "wukongim_ants_pool_running") {
    key = pool_key_from_labels(labels)
    remember_current_pool(key)
    current_running[key] = value
  } else if (name == "wukongim_ants_pool_capacity") {
    key = pool_key_from_labels(labels)
    remember_current_pool(key)
    current_capacity[key] = value
  } else if (name == "wukongim_ants_pool_waiting") {
    key = pool_key_from_labels(labels)
    remember_current_pool(key)
    current_waiting[key] = value
  } else if (name == "wukongim_ants_pool_utilization") {
    key = pool_key_from_labels(labels)
    remember_current_pool(key)
    current_utilization[key] = value
  }
}

END {
  flush_current_file()
  for (i = 1; i <= pool_count; i++) {
    key = pool_order[i]
    if (!(key in peak_seen)) {
      continue
    }
    printf "%s\t%s\t%s\t%s\t%.0f\t%.0f\t%.0f\t%.3f\n",
      tag,
      node,
      pool_component[key],
      pool_name[key],
      peak_running[key] + 0,
      peak_capacity[key] + 0,
      peak_waiting[key] + 0,
      peak_utilization[key] + 0
  }
}
