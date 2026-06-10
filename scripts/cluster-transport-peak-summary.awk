function metric_name(series, brace) {
  brace = index(series, "{")
  if (brace == 0) {
    return series
  }
  return substr(series, 1, brace - 1)
}

function sample_seq(path, parts, count, base) {
  count = split(path, parts, "/")
  base = parts[count]
  sub(/^.*-sample-/, "", base)
  sub(/[.]prom$/, "", base)
  return base + 0
}

function max_value(current, value) {
  if (value > current) {
    return value
  }
  return current
}

BEGIN {
  if (tag == "") {
    tag = "unknown"
  }
  interval_seconds = interval + 0
  if (interval_seconds <= 0) {
    interval_seconds = 1
  }
  mib = 1024 * 1024
}

/^[[:space:]]*#/ || NF < 2 {
  next
}

{
  seq = sample_seq(FILENAME)
  name = metric_name($1)
  value = $2 + 0
  seen[seq] = 1
  if (sample_points_seen[seq] != 1) {
    sample_points_seen[seq] = 1
    sample_points++
  }
  min_seq = (sample_points == 1 || seq < min_seq) ? seq : min_seq
  max_seq = max_value(max_seq, seq)

  if (name == "wukongim_transport_sent_bytes_total") {
    out_bytes[seq] += value
  } else if (name == "wukongim_transport_received_bytes_total") {
    in_bytes[seq] += value
  }
}

END {
  have_prev = 0
  for (seq = min_seq; seq <= max_seq; seq++) {
    if (!seen[seq]) {
      continue
    }
    if (have_prev) {
      elapsed = (seq - prev_seq) * interval_seconds
      if (elapsed <= 0) {
        elapsed = interval_seconds
      }
      out_delta = out_bytes[seq] - out_bytes[prev_seq]
      in_delta = in_bytes[seq] - in_bytes[prev_seq]
      if (out_delta < 0) {
        out_delta = 0
      }
      if (in_delta < 0) {
        in_delta = 0
      }
      out_rate = out_delta / elapsed
      in_rate = in_delta / elapsed
      duplex_rate = out_rate + in_rate
      cluster_rate = out_rate
      if (in_rate > cluster_rate) {
        cluster_rate = in_rate
      }
      sample_pairs++
      if (cluster_rate > peak_cluster_bps) {
        peak_cluster_bps = cluster_rate
        peak_out_bps = out_rate
        peak_in_bps = in_rate
        peak_duplex_bps = duplex_rate
        peak_from_seq = prev_seq
        peak_to_seq = seq
      }
    }
    prev_seq = seq
    have_prev = 1
  }

  printf "%s\t%.0f\t%.0f\t%.3f\t%.3f\t%.3f\t%.3f\t%.0f\t%.0f\n",
    tag,
    sample_points,
    sample_pairs,
    peak_cluster_bps / mib,
    peak_out_bps / mib,
    peak_in_bps / mib,
    peak_duplex_bps / mib,
    peak_from_seq,
    peak_to_seq
}
