#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TIMESTAMP="${WK_DYNAMIC_NODE_GATE_TIMESTAMP:-$(date '+%Y%m%d-%H%M%S')}"
DEFAULT_OUT_DIR="$ROOT_DIR/data/dynamic-node-readiness-gate/$TIMESTAMP"
GO_BIN="${WK_DYNAMIC_NODE_GATE_GO_BIN:-go}"
BUILD_GOFAIL_SCRIPT="${WK_DYNAMIC_NODE_GATE_BUILD_GOFAIL_SCRIPT:-scripts/build-gofail-binary.sh}"

PROFILE="quick"
DRY_RUN=0
REUSE_BINARY=0
OUT_DIR="$DEFAULT_OUT_DIR"
BINARY=""

usage() {
  cat <<'USAGE'
Usage: scripts/e2e/dynamic-node-readiness-gate.sh [options]

Runs the dynamic-node readiness gate test sequence and writes evidence into a
dedicated output directory.

Options:
  --profile quick|full|ops
                          Gate profile. quick skips Stage9D. Default: quick.
  --dry-run               Print resolved commands without executing them.
  --reuse-binary          Reuse an existing gofail binary and skip rebuilding it.
  --out-dir DIR           Evidence output directory.
  --binary PATH           Gofail-enabled cmd/wukongim binary path.
  -h, --help              Show this help.
USAGE
}

log() {
  printf '[dynamic-node-readiness-gate] %s\n' "$*"
}

die() {
  printf '[dynamic-node-readiness-gate] ERROR: %s\n' "$*" >&2
  exit 1
}

validate_profile() {
  case "$1" in
    quick|full|ops) ;;
    *)
      die "profile must be one of: quick, full, ops (got: $1)"
      ;;
  esac
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile)
      [[ $# -ge 2 ]] || die '--profile requires a value'
      PROFILE="$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --reuse-binary)
      REUSE_BINARY=1
      shift
      ;;
    --out-dir)
      [[ $# -ge 2 ]] || die '--out-dir requires a value'
      OUT_DIR="$2"
      shift 2
      ;;
    --binary)
      [[ $# -ge 2 ]] || die '--binary requires a value'
      BINARY="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      die "unknown option: $1"
      ;;
  esac
done

validate_profile "$PROFILE"

if [[ "$OUT_DIR" != /* ]]; then
  OUT_DIR="$ROOT_DIR/$OUT_DIR"
fi

if [[ -z "$BINARY" ]]; then
  BINARY="$OUT_DIR/wukongim-gofail"
elif [[ "$BINARY" != /* ]]; then
  BINARY="$ROOT_DIR/$BINARY"
fi

SUMMARY_FILE="$OUT_DIR/summary.md"
COMMAND_LOG="$OUT_DIR/commands.log"
ENVIRONMENT_FILE="$OUT_DIR/environment.md"

CONTROLLER_CMD=(env GOWORK=off "$GO_BIN" test ./pkg/controller -count=1)
FAULTS_DEFAULT_CMD=(env GOWORK=off "$GO_BIN" test -tags=e2e ./test/e2e/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1)
BUILD_GOFAIL_CMD=("$BUILD_GOFAIL_SCRIPT" --cmd ./cmd/wukongim --package internal/usecase/management --package pkg/controller --package pkg/cluster/tasks --package pkg/cluster/net --out "$BINARY")
STAGE10A_CMD=(env WK_E2E_BINARY="$BINARY" WK_E2E_GOFAIL_DYNAMIC_NODE=1 GOWORK=off "$GO_BIN" test -tags=e2e ./test/e2e/cluster/dynamic_node_faults -run "TestStage10A|TestGofailDynamicNodeBinaryExposesFailpoints" -count=1 -timeout 15m -p=1)
STAGE9D_CMD=(env GOWORK=off "$GO_BIN" test -tags=e2e ./test/e2e/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1)
WKCLI_CMD=(env GOWORK=off "$GO_BIN" test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1)
STAGE11_OPS_CMD=(env GOWORK=off "$GO_BIN" test -tags=e2e ./test/e2e/cluster/dynamic_node_operations -count=1 -timeout 12m -p=1)
DIFF_CHECK_CMD=(git diff --check)

CONTROLLER_CMD_TEXT="GOWORK=off $GO_BIN test ./pkg/controller -count=1"
FAULTS_DEFAULT_CMD_TEXT="GOWORK=off $GO_BIN test -tags=e2e ./test/e2e/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1"
BUILD_GOFAIL_CMD_TEXT="$BUILD_GOFAIL_SCRIPT --cmd ./cmd/wukongim --package internal/usecase/management --package pkg/controller --package pkg/cluster/tasks --package pkg/cluster/net --out $BINARY"
STAGE10A_CMD_TEXT="WK_E2E_BINARY=$BINARY WK_E2E_GOFAIL_DYNAMIC_NODE=1 GOWORK=off $GO_BIN test -tags=e2e ./test/e2e/cluster/dynamic_node_faults -run 'TestStage10A|TestGofailDynamicNodeBinaryExposesFailpoints' -count=1 -timeout 15m -p=1"
STAGE9D_CMD_TEXT="GOWORK=off $GO_BIN test -tags=e2e ./test/e2e/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1"
WKCLI_CMD_TEXT="GOWORK=off $GO_BIN test ./cmd/wkcli ./cmd/wkcli/internal/... -count=1"
STAGE11_OPS_CMD_TEXT="GOWORK=off $GO_BIN test -tags=e2e ./test/e2e/cluster/dynamic_node_operations -count=1 -timeout 12m -p=1"
DIFF_CHECK_CMD_TEXT="git diff --check"

print_plan() {
  printf 'root_dir=%s\n' "$ROOT_DIR"
  printf 'profile=%s\n' "$PROFILE"
  printf 'dry_run=%s\n' "$DRY_RUN"
  printf 'reuse_binary=%s\n' "$REUSE_BINARY"
  printf 'out_dir=%s\n' "$OUT_DIR"
  printf 'gofail_binary=%s\n' "$BINARY"
  printf 'summary=%s\n' "$SUMMARY_FILE"
  printf 'command_log=%s\n' "$COMMAND_LOG"
  printf 'environment=%s\n' "$ENVIRONMENT_FILE"
  printf 'controller_cmd=%s\n' "$CONTROLLER_CMD_TEXT"
  printf 'faults_default_cmd=%s\n' "$FAULTS_DEFAULT_CMD_TEXT"
  printf 'build_gofail_cmd=%s\n' "$BUILD_GOFAIL_CMD_TEXT"
  printf 'stage10a_cmd=%s\n' "$STAGE10A_CMD_TEXT"
  if [[ "$PROFILE" == "full" || "$PROFILE" == "ops" ]]; then
    printf 'stage9d_cmd=%s\n' "$STAGE9D_CMD_TEXT"
  fi
  if [[ "$PROFILE" == "ops" ]]; then
    printf 'wkcli_cmd=%s\n' "$WKCLI_CMD_TEXT"
    printf 'stage11_ops_cmd=%s\n' "$STAGE11_OPS_CMD_TEXT"
  fi
  printf 'diff_check_cmd=%s\n' "$DIFF_CHECK_CMD_TEXT"
}

run_step() {
  local name="$1"
  local display_cmd="$2"
  shift 2
  local step_log="$OUT_DIR/${name}.log"

  printf '## %s\n%s\n\n' "$name" "$display_cmd" >>"$COMMAND_LOG"
  log "running $name"
  if "$@" 2>&1 | tee "$step_log"; then
    printf -- '- %s: PASS\n' "$name" >>"$SUMMARY_FILE"
  else
    printf -- '- %s: FAIL\n' "$name" >>"$SUMMARY_FILE"
    log "step $name failed"
    printf '%s\n' "--- step log: $step_log ---" >&2
    tail -n 40 "$step_log" >&2 || true
    die "step failed: $name (see $step_log)"
  fi
}

if [[ "$DRY_RUN" -eq 1 ]]; then
  print_plan
  exit 0
fi

mkdir -p "$OUT_DIR"
cd "$ROOT_DIR"

cat >"$SUMMARY_FILE" <<EOF
# Dynamic Node Readiness Gate

- profile: $PROFILE
- out_dir: $OUT_DIR
- gofail_binary: $BINARY
- commands_log: $COMMAND_LOG

## Steps
EOF
: >"$COMMAND_LOG"
cat >"$ENVIRONMENT_FILE" <<EOF
# Dynamic Node Readiness Gate Environment

- profile: $PROFILE
- root_dir: $ROOT_DIR
- out_dir: $OUT_DIR
- go_bin: $GO_BIN
- build_gofail_script: $BUILD_GOFAIL_SCRIPT
- gofail_binary: $BINARY
EOF

if [[ "$REUSE_BINARY" -eq 1 && ! -x "$BINARY" ]]; then
  die "--reuse-binary requires an existing executable binary: $BINARY"
fi

run_step "controller" "$CONTROLLER_CMD_TEXT" "${CONTROLLER_CMD[@]}"
run_step "dynamic-node-faults-default" "$FAULTS_DEFAULT_CMD_TEXT" "${FAULTS_DEFAULT_CMD[@]}"
if [[ "$REUSE_BINARY" -eq 0 ]]; then
  run_step "build-gofail" "$BUILD_GOFAIL_CMD_TEXT" "${BUILD_GOFAIL_CMD[@]}"
else
  printf '## %s\n%s\n\n' "build-gofail" "$BUILD_GOFAIL_CMD_TEXT" >>"$COMMAND_LOG"
  printf -- '- %s: SKIP (reuse-binary)\n' "build-gofail" >>"$SUMMARY_FILE"
fi
run_step "stage10a-gofail" "$STAGE10A_CMD_TEXT" "${STAGE10A_CMD[@]}"
if [[ "$PROFILE" == "full" || "$PROFILE" == "ops" ]]; then
  run_step "stage9d-real-traffic" "$STAGE9D_CMD_TEXT" "${STAGE9D_CMD[@]}"
fi
if [[ "$PROFILE" == "ops" ]]; then
  run_step "wkcli" "$WKCLI_CMD_TEXT" "${WKCLI_CMD[@]}"
  run_step "stage11-ops" "$STAGE11_OPS_CMD_TEXT" "${STAGE11_OPS_CMD[@]}"
fi
run_step "diff-check" "$DIFF_CHECK_CMD_TEXT" "${DIFF_CHECK_CMD[@]}"

log "summary: $SUMMARY_FILE"
log "commands: $COMMAND_LOG"
