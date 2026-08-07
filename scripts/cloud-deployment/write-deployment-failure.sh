#!/usr/bin/env bash
set -euo pipefail

if (($# != 6)); then
  echo "usage: write-deployment-failure.sh OUTPUT CODE GATE ROLE EVIDENCE HOST_STATE" >&2
  exit 2
fi

output="$1"
code="$2"
gate="$3"
role="$4"
evidence="$5"
host_state="$6"

case "$code" in
  artifact_provenance_invalid|artifact_download_failed|credential_materialization_failed|credential_cleanup_failed|\
    invalid_plan|bundle_transfer_failed|bundle_digest_mismatch|host_identity_invalid|\
    base_tools_missing|data_disk_mount_invalid|filesystem_capacity_insufficient|\
    filesystem_free_space_low|time_drift_exceeded|native_activation_failed|\
    systemd_service_inactive|cluster_membership_unready|slot_topology_unready|\
    workers_unready|prometheus_targets_unready|workload_config_invalid|\
    public_endpoints_unready|analysis_unready|readiness_evidence_invalid) ;;
  *) echo "invalid deployment failure code" >&2; exit 2 ;;
esac
case "$gate" in
  none|plan_validated|bundle_transferred|bundle_verified|hosts_prepared|\
    services_active|cluster_converged|ready) ;;
  *) echo "invalid deployment gate" >&2; exit 2 ;;
esac
case "$role" in
  ""|service-1|service-2|service-3|load) ;;
  *) echo "invalid deployment host role" >&2; exit 2 ;;
esac
[[ -n "$evidence" && ${#evidence} -le 256 && "$evidence" != *$'\n'* ]]
[[ -n "$host_state" && ${#host_state} -le 256 && "$host_state" != *$'\n'* ]]

umask 077
temporary="${output}.tmp.$$"
trap 'rm -f "$temporary"' EXIT
jq -n \
  --arg code "$code" --arg gate "$gate" --arg role "$role" \
  --arg evidence "$evidence" --arg host_state "$host_state" \
  '{passed:false,failure:({schema:"wukongim.cloud_deployment.failure/v1",code:$code,
    last_completed_gate:$gate,evidence:[$evidence,$host_state]}
    + (if $role == "" then {} else {host_role:$role} end))}' >"$temporary"
chmod 0600 "$temporary"
mv "$temporary" "$output"
trap - EXIT
