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
  series = $1
  value = $2 + 0
  phase = "after"
  if (FILENAME == ARGV[1]) {
    phase = "before"
  }
  name = metric_name(series)
  labels = metric_labels(series)
  result = label_value(labels, "result")
  stage = label_value(labels, "stage")
  path = label_value(labels, "path")
  kind = label_value(labels, "kind")

  if (phase == "after") {
    if (name == "wukongim_channelwrite_reactor_mailbox_depth") {
      mailbox_depth_max = max_value(mailbox_depth_max, value)
    } else if (name == "wukongim_channelwrite_reactor_mailbox_capacity") {
      mailbox_capacity_max = max_value(mailbox_capacity_max, value)
    } else if (name == "wukongim_channelwrite_reactor_effect_slots") {
      effect_slots_max = max_value(effect_slots_max, value)
    } else if (name == "wukongim_channelwrite_reactor_effect_slots_capacity") {
      effect_slots_capacity_max = max_value(effect_slots_capacity_max, value)
    } else if (name == "wukongim_channelwrite_reactor_state_items") {
      if (kind == "pending_append") {
        pending_append_max = max_value(pending_append_max, value)
      } else if (kind == "append_inflight") {
        append_inflight_max = max_value(append_inflight_max, value)
      } else if (kind == "post_commit_backlog") {
        post_commit_backlog_max = max_value(post_commit_backlog_max, value)
      }
    } else if (name == "wukongim_channelwrite_effect_worker_inflight") {
      effect_worker_inflight_max = max_value(effect_worker_inflight_max, value)
    } else if (name == "wukongim_channelwrite_effect_worker_capacity") {
      effect_worker_capacity_max = max_value(effect_worker_capacity_max, value)
    } else if (name == "wukongim_channelwrite_effect_queue_depth") {
      effect_queue_depth_max = max_value(effect_queue_depth_max, value)
    } else if (name == "wukongim_channelwrite_effect_queue_capacity") {
      effect_queue_capacity_max = max_value(effect_queue_capacity_max, value)
    }
  }

  if (name == "wukongim_channelwrite_router_total") {
    add_counter("router_total", phase, value)
    add_counter("router_path:" path, phase, value)
    add_counter("router_result:" result, phase, value)
    if (result != "ok") {
      add_counter("router_errors", phase, value)
    }
  } else if (name == "wukongim_channelwrite_router_duration_seconds_count") {
    add_counter("router_duration_count", phase, value)
  } else if (name == "wukongim_channelwrite_router_duration_seconds_sum") {
    add_counter("router_duration_sum", phase, value)
  } else if (name == "wukongim_channelwrite_local_admission_total") {
    add_counter("local_admission_total", phase, value)
    add_counter("local_admission_result:" result, phase, value)
    if (result != "accepted") {
      add_counter("local_admission_rejected", phase, value)
    }
  } else if (name == "wukongim_channelwrite_effect_total") {
    add_counter("effect_total", phase, value)
    add_counter("effect_stage:" stage, phase, value)
    add_counter("effect_result:" result, phase, value)
    if (result != "ok") {
      add_counter("effect_errors", phase, value)
    }
  } else if (name == "wukongim_channelwrite_effect_duration_seconds_count") {
    add_counter("effect_duration_count", phase, value)
  } else if (name == "wukongim_channelwrite_effect_duration_seconds_sum") {
    add_counter("effect_duration_sum", phase, value)
  }
}

END {
  mailbox_fill_max = 0
  if (mailbox_capacity_max > 0) {
    mailbox_fill_max = mailbox_depth_max / mailbox_capacity_max
  }
  effect_slots_fill_max = 0
  if (effect_slots_capacity_max > 0) {
    effect_slots_fill_max = effect_slots_max / effect_slots_capacity_max
  }
  effect_worker_util_max = 0
  if (effect_worker_capacity_max > 0) {
    effect_worker_util_max = effect_worker_inflight_max / effect_worker_capacity_max
  }
  effect_queue_fill_max = 0
  if (effect_queue_capacity_max > 0) {
    effect_queue_fill_max = effect_queue_depth_max / effect_queue_capacity_max
  }

  printf "%s\t%s\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.3f\t%.0f\t%.0f\t%.3f\n",
    tag,
    node,
    counter_delta("router_total"),
    counter_delta("router_path:local"),
    counter_delta("router_path:remote"),
    counter_delta("router_errors"),
    counter_delta("router_result:backpressured"),
    counter_delta("router_result:channel_busy"),
    counter_delta("router_result:route_not_ready"),
    counter_delta("router_result:timeout"),
    counter_delta("local_admission_total"),
    counter_delta("local_admission_rejected"),
    avg_delta_ms("router_duration_sum", "router_duration_count"),
    mailbox_depth_max,
    mailbox_capacity_max,
    mailbox_fill_max,
    effect_slots_max,
    effect_slots_capacity_max,
    pending_append_max,
    append_inflight_max,
    post_commit_backlog_max,
    counter_delta("effect_total"),
    counter_delta("effect_errors"),
    counter_delta("effect_stage:append"),
    counter_delta("effect_stage:post_commit"),
    avg_delta_ms("effect_duration_sum", "effect_duration_count"),
    effect_worker_inflight_max,
    effect_worker_capacity_max,
    effect_worker_util_max,
    effect_queue_depth_max,
    effect_queue_capacity_max,
    effect_queue_fill_max
}
