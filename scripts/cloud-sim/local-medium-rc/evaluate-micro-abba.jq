def mean:
  if length == 0 then 0 else add / length end;

def cv:
  . as $samples
  | ($samples | mean) as $mean
  | if $mean == 0 then
      if all($samples[]; . == 0) then 0 else 1 end
    else
      (([$samples[] | (. - $mean) * (. - $mean)] | add / ($samples | length)) | sqrt) / $mean
    end;

def drift($left; $right):
  (($left + $right) / 2) as $middle
  | if $middle == 0 then
      if $left == $right then 0 else 1 end
    else
      (($left - $right) | if . < 0 then -. else . end) / $middle
    end;

def ratio($candidate; $baseline):
  if $baseline > 0 then $candidate / $baseline
  elif $candidate == 0 then 1
  else null
  end;

def metric($case_id; $metric_id):
  .cases[] | select(.id == $case_id) | .metrics[$metric_id];

def leg_means($metric):
  {
    A1: ($metric.samples_by_leg.A1 | mean),
    B1: ($metric.samples_by_leg.B1 | mean),
    B2: ($metric.samples_by_leg.B2 | mean),
    A2: ($metric.samples_by_leg.A2 | mean)
  };

def leg_cvs($metric):
  {
    A1: ($metric.samples_by_leg.A1 | cv),
    B1: ($metric.samples_by_leg.B1 | cv),
    B2: ($metric.samples_by_leg.B2 | cv),
    A2: ($metric.samples_by_leg.A2 | cv)
  };

def metric_measurement($metric):
  (leg_means($metric)) as $means
  | (leg_cvs($metric)) as $cvs
  | (($metric.samples_by_leg.A1 + $metric.samples_by_leg.A2) | mean) as $baseline
  | (($metric.samples_by_leg.B1 + $metric.samples_by_leg.B2) | mean) as $candidate
  | {
      unit: $metric.unit,
      means_by_leg: $means,
      cv_by_leg: $cvs,
      pair_drift: {
        baseline: drift($means.A1; $means.A2),
        candidate: drift($means.B1; $means.B2)
      },
      baseline_mean: $baseline,
      candidate_mean: $candidate,
      candidate_to_baseline_ratio: ratio($candidate; $baseline)
    };

def stable($measurement; $maximum_cv; $maximum_drift):
  all($measurement.cv_by_leg[]; . <= $maximum_cv) and
  $measurement.pair_drift.baseline <= $maximum_drift and
  $measurement.pair_drift.candidate <= $maximum_drift;

def ratio_at_most($measurement; $maximum):
  ($measurement.candidate_to_baseline_ratio | type == "number") and
  $measurement.candidate_to_baseline_ratio <= $maximum;

def samples_valid($samples; $count; $strictly_positive):
  ($samples | type == "array" and length == $count) and
  (if $strictly_positive then
     all($samples[]; type == "number" and . > 0)
   else
     all($samples[]; type == "number" and . >= 0)
   end);

def metric_valid($metric; $unit; $count; $strictly_positive):
  $metric.unit == $unit and
  ($metric.samples_by_leg | keys | sort) == ["A1", "A2", "B1", "B2"] and
  all($metric.samples_by_leg[]; samples_valid(.; $count; $strictly_positive));

def case_valid($case; $id; $count):
  $case.id == $id and
  ($case.metrics | keys | sort) == ["allocs_per_op", "bytes_per_op", "ns_per_op"] and
  metric_valid($case.metrics.ns_per_op; "ns/op"; $count; true) and
  metric_valid($case.metrics.bytes_per_op; "B/op"; $count; false) and
  metric_valid($case.metrics.allocs_per_op; "allocs/op"; $count; false);

if .schema != "wukongim/local-medium-rc-micro-samples/v1" or
   .protocol.order != ["A1", "B1", "B2", "A2"] or
   (.protocol.samples_per_leg | type != "number" or floor != . or . < 6) or
   (.protocol.benchtime_seconds | type != "number" or floor != . or . < 5) or
   .protocol.gomaxprocs != 4 or
   .thresholds != {
     maximum_cv:0.05,
     maximum_pair_drift_ratio:0.05,
     maximum_authority_ns_ratio:0.5574,
     maximum_non_regression_ratio:1.05
   } or
   (.cases | type != "array" or length != 2) or
   ([.cases[].id] | sort) != ["authority_resolve", "owner_push_ack"] or
   ([.cases[].id] | unique | length) != 2 or
   ((.cases[] | select(.id == "authority_resolve")) as $case |
     (case_valid($case; "authority_resolve"; .protocol.samples_per_leg) | not)) or
   ((.cases[] | select(.id == "owner_push_ack")) as $case |
     (case_valid($case; "owner_push_ack"; .protocol.samples_per_leg) | not))
then
  error("invalid micro sample contract")
else
  . as $input
  | (metric("authority_resolve"; "ns_per_op") | metric_measurement(.)) as $authority_ns
  | (metric("authority_resolve"; "bytes_per_op") | metric_measurement(.)) as $authority_bytes
  | (metric("authority_resolve"; "allocs_per_op") | metric_measurement(.)) as $authority_allocs
  | (metric("owner_push_ack"; "ns_per_op") | metric_measurement(.)) as $owner_ns
  | (metric("owner_push_ack"; "bytes_per_op") | metric_measurement(.)) as $owner_bytes
  | (metric("owner_push_ack"; "allocs_per_op") | metric_measurement(.)) as $owner_allocs
  | {
      schema: "wukongim/local-medium-rc-micro-verdict/v1",
      scope: "revision_neutral_micro_only",
      cloud_authorized: false,
      protocol: $input.protocol,
      build_smoke: $input.build_smoke,
      run: $input.run,
      thresholds: $input.thresholds,
      measurements: {
        authority_resolve: {
          ns_per_op: $authority_ns,
          bytes_per_op: $authority_bytes,
          allocs_per_op: $authority_allocs
        },
        owner_push_ack: {
          ns_per_op: $owner_ns,
          bytes_per_op: $owner_bytes,
          allocs_per_op: $owner_allocs
        }
      },
      checks: {
        exact_abba_order: ($input.protocol.order == ["A1", "B1", "B2", "A2"]),
        minimum_six_samples_per_leg: ($input.protocol.samples_per_leg >= 6),
        authority_ns_stable: stable($authority_ns; $input.thresholds.maximum_cv; $input.thresholds.maximum_pair_drift_ratio),
        authority_bytes_stable: stable($authority_bytes; $input.thresholds.maximum_cv; $input.thresholds.maximum_pair_drift_ratio),
        authority_allocs_stable: stable($authority_allocs; $input.thresholds.maximum_cv; $input.thresholds.maximum_pair_drift_ratio),
        owner_ns_stable: stable($owner_ns; $input.thresholds.maximum_cv; $input.thresholds.maximum_pair_drift_ratio),
        owner_bytes_stable: stable($owner_bytes; $input.thresholds.maximum_cv; $input.thresholds.maximum_pair_drift_ratio),
        owner_allocs_stable: stable($owner_allocs; $input.thresholds.maximum_cv; $input.thresholds.maximum_pair_drift_ratio),
        authority_ns_improves_with_required_margin: ratio_at_most($authority_ns; $input.thresholds.maximum_authority_ns_ratio),
        authority_bytes_no_regression: ratio_at_most($authority_bytes; $input.thresholds.maximum_non_regression_ratio),
        authority_allocs_no_regression: ratio_at_most($authority_allocs; $input.thresholds.maximum_non_regression_ratio),
        owner_ns_no_regression: ratio_at_most($owner_ns; $input.thresholds.maximum_non_regression_ratio),
        owner_bytes_no_regression: ratio_at_most($owner_bytes; $input.thresholds.maximum_non_regression_ratio),
        owner_allocs_no_regression: ratio_at_most($owner_allocs; $input.thresholds.maximum_non_regression_ratio)
      }
    }
  | .micro_pass = all(.checks[]; . == true)
  | .decision = (if .micro_pass then "micro_pass" else "micro_fail" end)
end
