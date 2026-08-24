BEGIN {
  OFS = "\t"
  if (header) {
    print "tag", "host", "evidence", "physical_device", "iops_available", "iops_max", \
      "bytes_per_second_available", "bytes_per_second_max", "utilization_available", \
      "utilization_percent_max", "service_time_available", "service_time_milliseconds_max", \
      "read_write_split_available"
    exit
  }
}

function label_value(line, name, marker, tail) {
  marker = name "=\""
  if (index(line, marker) == 0) return ""
  tail = substr(line, index(line, marker) + length(marker))
  sub(/\".*/, "", tail)
  return tail
}

function observe_device(candidate) {
  if (candidate == "") return
  if (device != "" && device != candidate) evidence = "missing"
  device = candidate
}

function observe_max(key, value) {
  if (!(key in maximum_seen) || value > maxima[key]) maxima[key] = value
  maximum_seen[key] = 1
}

/^wkbench_host_block_io_schema_info\{/ {
  if (label_value($0, "version") != "v1") evidence = "missing"
  schema_seen = 1
  observe_device(label_value($0, "physical_device"))
  next
}

/^wkbench_host_block_io_available\{/ {
  field = label_value($0, "field")
  observe_device(label_value($0, "physical_device"))
  availability_seen[field] = 1
  if (($NF + 0) > availability[field]) availability[field] = $NF + 0
  next
}

/^wkbench_host_block_io_iops\{/ {
  observe_device(label_value($0, "physical_device"))
  if (label_value($0, "operation") == "total") observe_max("iops", $NF + 0)
  next
}

/^wkbench_host_block_io_bytes_per_second\{/ {
  observe_device(label_value($0, "physical_device"))
  if (label_value($0, "operation") == "total") observe_max("bytes", $NF + 0)
  next
}

/^wkbench_host_block_io_utilization_percent\{/ {
  observe_device(label_value($0, "physical_device"))
  observe_max("utilization", $NF + 0)
  next
}

/^wkbench_host_block_io_service_time_milliseconds\{/ {
  observe_device(label_value($0, "physical_device"))
  observe_max("service_time", $NF + 0)
  next
}

END {
  if (header) exit
  if (evidence == "") evidence = "complete"
  if (!schema_seen || device == "") evidence = "missing"
  fields[1] = "iops"
  fields[2] = "bytes_per_second"
  fields[3] = "utilization"
  fields[4] = "service_time"
  fields[5] = "read_write_split"
  for (field_index = 1; field_index <= 5; field_index++) {
    if (!(fields[field_index] in availability_seen)) evidence = "missing"
  }
  if (availability["iops"] && !("iops" in maximum_seen)) evidence = "missing"
  if (availability["bytes_per_second"] && !("bytes" in maximum_seen)) evidence = "missing"
  if (availability["utilization"] && !("utilization" in maximum_seen)) evidence = "missing"
  if (availability["service_time"] && !("service_time" in maximum_seen)) evidence = "missing"
  if (evidence == "complete" && !availability["iops"] && !availability["bytes_per_second"] && \
      !availability["utilization"] && !availability["service_time"]) evidence = "unavailable"

  iops = availability["iops"] ? sprintf("%.6f", maxima["iops"]) : "unavailable"
  bytes = availability["bytes_per_second"] ? sprintf("%.6f", maxima["bytes"]) : "unavailable"
  utilization = availability["utilization"] ? sprintf("%.6f", maxima["utilization"]) : "unavailable"
  service_time = availability["service_time"] ? sprintf("%.6f", maxima["service_time"]) : "unavailable"
  if (device == "") device = "unavailable"
  print tag, host, evidence, device, availability["iops"] + 0, iops, \
    availability["bytes_per_second"] + 0, bytes, availability["utilization"] + 0, utilization, \
    availability["service_time"] + 0, service_time, availability["read_write_split"] + 0
}
