#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

for script in run-micro-abba.sh evaluate-micro-abba.sh test-micro-abba-static.sh; do
  bash -n "$SCRIPT_DIR/$script"
done
command -v jq >/dev/null 2>&1 || { printf 'jq is required for the micro policy test\n' >&2; exit 1; }

for executable in run-micro-abba.sh evaluate-micro-abba.sh; do
  if grep -Eq 'gh[[:space:]]+workflow[[:space:]]+run|wkcloudsim[[:space:]]+(create|provision)|cloud-sim-provision' "$SCRIPT_DIR/$executable"; then
    printf '%s contains a forbidden cloud mutation command\n' "$executable" >&2
    exit 1
  fi
done

samples() {
  local value="$1"
  jq -cn --argjson value "$value" '[range(0;6) | $value]'
}

metric() {
  local unit="$1" baseline="$2" candidate="$3"
  jq -cn --arg unit "$unit" --argjson A "$(samples "$baseline")" --argjson B "$(samples "$candidate")" \
    '{unit:$unit,samples_by_leg:{A1:$A,B1:$B,B2:$B,A2:$A}}'
}

case_json() {
  local id="$1" baseline_ns="$2" candidate_ns="$3" baseline_bytes="$4" candidate_bytes="$5" baseline_allocs="$6" candidate_allocs="$7"
  jq -cn --arg id "$id" \
    --argjson ns "$(metric ns/op "$baseline_ns" "$candidate_ns")" \
    --argjson bytes "$(metric B/op "$baseline_bytes" "$candidate_bytes")" \
    --argjson allocs "$(metric allocs/op "$baseline_allocs" "$candidate_allocs")" \
    '{id:$id,metrics:{ns_per_op:$ns,bytes_per_op:$bytes,allocs_per_op:$allocs}}'
}

jq -n \
  --argjson authority "$(case_json authority_resolve 100 50 100 100 10 10)" \
  --argjson owner "$(case_json owner_push_ack 200 201 200 200 20 20)" '
  {
    schema:"wukongim/local-medium-rc-micro-samples/v1",
    build_smoke:{manifest_sha256:("a"*64),baseline_source_sha:("b"*40),candidate_source_sha:("c"*40)},
    protocol:{order:["A1","B1","B2","A2"],samples_per_leg:6,benchtime_seconds:5,gomaxprocs:4},
    thresholds:{maximum_cv:0.05,maximum_pair_drift_ratio:0.05,maximum_authority_ns_ratio:0.5574,maximum_non_regression_ratio:1.05},
    cases:[$authority,$owner]
  }' >"${TMPDIR:-/tmp}/wk-local-medium-rc-policy-pass.$$.json"
fixture="${TMPDIR:-/tmp}/wk-local-medium-rc-policy-pass.$$.json"
manifest_fixture="$fixture.manifest"
failure_fixture_root="${TMPDIR:-/tmp}/wk-local-medium-rc-fail-close.$$"
trap 'rm -f "$fixture" "$fixture.fail" "$manifest_fixture" "$manifest_fixture.fail"; rm -rf "$failure_fixture_root"' EXIT

jq -f "$SCRIPT_DIR/evaluate-micro-abba.jq" "$fixture" |
  jq -e '.micro_pass == true and .decision == "micro_pass" and .cloud_authorized == false' >/dev/null

jq '(.cases[] | select(.id == "authority_resolve") | .metrics.ns_per_op.samples_by_leg.B2) = [90,90,90,90,90,90]' \
  "$fixture" >"$fixture.fail"
jq -f "$SCRIPT_DIR/evaluate-micro-abba.jq" "$fixture.fail" |
  jq -e '.micro_pass == false and .decision == "micro_fail" and .checks.authority_ns_stable == false' >/dev/null

jq '(.cases[] | select(.id == "authority_resolve") | .metrics.ns_per_op.samples_by_leg.B1) = [56,56,56,56,56,56] |
    (.cases[] | select(.id == "authority_resolve") | .metrics.ns_per_op.samples_by_leg.B2) = [56,56,56,56,56,56]' \
  "$fixture" >"$fixture.fail"
jq -f "$SCRIPT_DIR/evaluate-micro-abba.jq" "$fixture.fail" |
  jq -e '.micro_pass == false and .cloud_authorized == false and .checks.authority_ns_improves_with_required_margin == false' >/dev/null

jq '(.cases[] | select(.id == "owner_push_ack") | .metrics.bytes_per_op.samples_by_leg.B1) = [211,211,211,211,211,211] |
    (.cases[] | select(.id == "owner_push_ack") | .metrics.bytes_per_op.samples_by_leg.B2) = [211,211,211,211,211,211]' \
  "$fixture" >"$fixture.fail"
jq -f "$SCRIPT_DIR/evaluate-micro-abba.jq" "$fixture.fail" |
  jq -e '.micro_pass == false and .cloud_authorized == false and .checks.owner_bytes_no_regression == false' >/dev/null

jq '(.cases[] | select(.id == "authority_resolve") | .metrics.ns_per_op.samples_by_leg.A1) = [100,100,100,100,100]' \
  "$fixture" >"$fixture.fail"
if jq -f "$SCRIPT_DIR/evaluate-micro-abba.jq" "$fixture.fail" >/dev/null 2>&1; then
  printf 'short sample set unexpectedly passed the policy contract\n' >&2
  exit 1
fi

jq '.thresholds.maximum_authority_ns_ratio = 1.0' "$fixture" >"$fixture.fail"
if jq -f "$SCRIPT_DIR/evaluate-micro-abba.jq" "$fixture.fail" >/dev/null 2>&1; then
  printf 'tampered threshold unexpectedly passed the policy contract\n' >&2
  exit 1
fi

jq -n '{
  schema:"wukongim/local-medium-rc-revision-neutral-build-smoke/v1",status:"passed",
  baseline_source_sha:("a"*40),candidate_source_sha:("b"*40),
  baseline_lock:{
    path:"scripts/cloud-sim/local-medium-rc/baseline-lock.json",sha256:("9"*64),
    schema:"wukongim/local-medium-rc-baseline-lock/v1",baseline_source_sha:("a"*40),source_run_identity:"gh-29889127179-1"
  },
  toolchain:{
    path:"/goroot/bin/go",version:"go version go1.25.11 darwin/arm64",sha256:("c"*64),gotoolchain:"local",
    goroot:{path:"/goroot",gohostos:"darwin",gohostarch:"arm64"},
    tools:{
      compile:{path:"/goroot/pkg/tool/darwin_arm64/compile",sha256:("5"*64)},
      link:{path:"/goroot/pkg/tool/darwin_arm64/link",sha256:("6"*64)},
      asm:{path:"/goroot/pkg/tool/darwin_arm64/asm",sha256:("7"*64)}
    }
  },
  common_workload:{path:"scripts/cloud-sim/local-medium-rc/workload_test.go.overlay",sha256:("d"*64)},
  adapters:{
    baseline:{path:"scripts/cloud-sim/local-medium-rc/adapter_baseline_test.go.overlay",sha256:("e"*64)},
    candidate:{path:"scripts/cloud-sim/local-medium-rc/adapter_candidate_test.go.overlay",sha256:("f"*64)}
  },
  shape:{physical_hash_slots:256,logical_slots:10,recipients:512,target_groups:221,online_routes:55,payload_bytes:256},
  benchmarks:["BenchmarkLocalMediumRCRevisionNeutralAuthorityResolve512x256","BenchmarkLocalMediumRCRevisionNeutralOwnerPushAck512x221x55"],
  binaries:{baseline:{path:"bin/app-baseline.test",sha256:("1"*64)},candidate:{path:"bin/app-candidate.test",sha256:("2"*64)}},
  equivalence:{baseline:{path:"equivalence/baseline.txt",sha256:("3"*64)},candidate:{path:"equivalence/candidate.txt",sha256:("4"*64)}}
}' >"$manifest_fixture"
jq -e -f "$SCRIPT_DIR/validate-build-smoke-manifest.jq" "$manifest_fixture" >/dev/null
for tamper in unexpected_top_level missing_adapters missing_equivalence changed_overlay_path missing_baseline_lock changed_locked_baseline missing_compile relative_goroot wrong_toolchain_selection wrong_go_version; do
  case "$tamper" in
    unexpected_top_level) jq '.unreviewed = true' "$manifest_fixture" >"$manifest_fixture.fail" ;;
    missing_adapters) jq 'del(.adapters)' "$manifest_fixture" >"$manifest_fixture.fail" ;;
    missing_equivalence) jq 'del(.equivalence)' "$manifest_fixture" >"$manifest_fixture.fail" ;;
    changed_overlay_path) jq '.common_workload.path = "tmp/workload.go"' "$manifest_fixture" >"$manifest_fixture.fail" ;;
    missing_baseline_lock) jq 'del(.baseline_lock)' "$manifest_fixture" >"$manifest_fixture.fail" ;;
    changed_locked_baseline) jq '.baseline_lock.baseline_source_sha = ("f"*40)' "$manifest_fixture" >"$manifest_fixture.fail" ;;
    missing_compile) jq 'del(.toolchain.tools.compile)' "$manifest_fixture" >"$manifest_fixture.fail" ;;
    relative_goroot) jq '.toolchain.goroot.path = "goroot"' "$manifest_fixture" >"$manifest_fixture.fail" ;;
    wrong_toolchain_selection) jq '.toolchain.gotoolchain = "auto"' "$manifest_fixture" >"$manifest_fixture.fail" ;;
    wrong_go_version) jq '.toolchain.version = "go version go1.26.4 darwin/arm64"' "$manifest_fixture" >"$manifest_fixture.fail" ;;
  esac
  if jq -e -f "$SCRIPT_DIR/validate-build-smoke-manifest.jq" "$manifest_fixture.fail" >/dev/null 2>&1; then
    printf 'tampered build-smoke manifest unexpectedly passed: %s\n' "$tamper" >&2
    exit 1
  fi
done

mkdir -p "$failure_fixture_root/build" "$failure_fixture_root/run"
jq -n '{schema:"wukongim/local-medium-rc-micro-verdict/v1",decision:"micro_pass",micro_pass:true,cloud_authorized:true}' \
  >"$failure_fixture_root/run/micro-verdict.json"
if "$SCRIPT_DIR/evaluate-micro-abba.sh" "$failure_fixture_root/build" "$failure_fixture_root/run" >/dev/null 2>&1; then
  printf 'fail-close evaluator fixture unexpectedly passed\n' >&2
  exit 1
fi
jq -e '
  .schema == "wukongim/local-medium-rc-micro-verdict/v1" and
  .decision == "micro_fail" and .micro_pass == false and .cloud_authorized == false and
  (.validation_error | type == "string" and length > 0)
' "$failure_fixture_root/run/micro-verdict.json" >/dev/null

printf 'local-medium-rc-micro-static: PASS\n'
