#!/usr/bin/env bash
set -euo pipefail

GOFAIL_VERSION="go.etcd.io/gofail@v0.2.0"
OUT_PATH=""
WORK_DIR=""
KEEP_WORK=0
DRY_RUN=0
FAILPOINT_PACKAGES=("pkg/transport")
COPY_EXCLUDES=(".git" ".worktrees")

usage() {
  cat <<'USAGE'
Usage: scripts/build-gofail-binary.sh [options]

Builds a failpoint-enabled cmd/wukongim binary from a temporary source copy.

Options:
  --out PATH              Output binary path. Defaults to /tmp/wukongim-gofail-<pid>.
  --work-dir DIR          Use DIR as the temporary source copy. Must not already exist.
  --package DIR           Add a directory passed to `gofail enable`. Repeatable.
  --gofail-version VER    gofail module version. Default: go.etcd.io/gofail@v0.2.0.
  --keep-work             Keep the temporary source copy after the build.
  --dry-run               Print resolved commands without copying or building.
  -h, --help              Show this help.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out)
      OUT_PATH="${2:?missing value for --out}"
      shift 2
      ;;
    --work-dir)
      WORK_DIR="${2:?missing value for --work-dir}"
      shift 2
      ;;
    --package)
      FAILPOINT_PACKAGES+=("${2:?missing value for --package}")
      shift 2
      ;;
    --gofail-version)
      GOFAIL_VERSION="${2:?missing value for --gofail-version}"
      shift 2
      ;;
    --keep-work)
      KEEP_WORK=1
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ -z "$OUT_PATH" ]]; then
  OUT_PATH="/tmp/wukongim-gofail-$$"
fi
if [[ -z "$WORK_DIR" ]]; then
  WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/wukongim-gofail-src.XXXXXX")"
  remove_work_dir=1
else
  remove_work_dir=0
fi
GOFAIL_MODULE="${GOFAIL_VERSION%@*}"
GOFAIL_TAG="${GOFAIL_VERSION##*@}"
if [[ "$GOFAIL_MODULE" == "$GOFAIL_VERSION" ]]; then
  GOFAIL_TAG="latest"
fi
GOFAIL_RUNTIME_DEP="${GOFAIL_MODULE}/runtime@${GOFAIL_TAG}"

join_by_space() {
  local IFS=" "
  echo "$*"
}

print_plan() {
  echo "repo_root=$repo_root"
  echo "source_dir=$WORK_DIR"
  echo "output=$OUT_PATH"
  echo "gofail_version=$GOFAIL_VERSION"
  echo "failpoint_packages=$(join_by_space "${FAILPOINT_PACKAGES[@]}")"
  echo "copy_excludes=$(join_by_space "${COPY_EXCLUDES[@]}")"
  echo "enable_cmd=gofail enable $(join_by_space "${FAILPOINT_PACKAGES[@]}")"
  echo "runtime_dep_cmd=GOWORK=off go get $GOFAIL_RUNTIME_DEP"
  echo "build_cmd=GOWORK=off go build -o $OUT_PATH ./cmd/wukongim"
}

if [[ "$DRY_RUN" -eq 1 ]]; then
  print_plan
  exit 0
fi

if [[ "$remove_work_dir" -eq 0 && -e "$WORK_DIR" ]]; then
  echo "--work-dir already exists: $WORK_DIR" >&2
  exit 2
fi

if [[ "$remove_work_dir" -eq 0 ]]; then
  mkdir -p "$(dirname "$WORK_DIR")"
  mkdir "$WORK_DIR"
fi
if [[ "$KEEP_WORK" -eq 0 ]]; then
  cleanup() {
    rm -rf "$WORK_DIR"
  }
  trap cleanup EXIT
fi

copy_args=()
for exclude in "${COPY_EXCLUDES[@]}"; do
  copy_args+=("--exclude=$exclude")
done

echo "copying source to $WORK_DIR"
(
  cd "$repo_root"
  LC_ALL=C tar "${copy_args[@]}" -cf - .
) | (
  cd "$WORK_DIR"
  LC_ALL=C tar -xf -
)

gofail_bin_dir="$(mktemp -d "${TMPDIR:-/tmp}/wukongim-gofail-bin.XXXXXX")"
trap 'rm -rf "$gofail_bin_dir"; [[ "$KEEP_WORK" -eq 0 ]] && rm -rf "$WORK_DIR"' EXIT

echo "installing $GOFAIL_VERSION"
GOWORK=off GOBIN="$gofail_bin_dir" go install "$GOFAIL_VERSION"

echo "enabling failpoints: $(join_by_space "${FAILPOINT_PACKAGES[@]}")"
(
  cd "$WORK_DIR"
  PATH="$gofail_bin_dir:$PATH" gofail enable "${FAILPOINT_PACKAGES[@]}"
)

echo "pinning failpoint runtime: $GOFAIL_RUNTIME_DEP"
(
  cd "$WORK_DIR"
  GOWORK=off go get "$GOFAIL_RUNTIME_DEP"
)

echo "building failpoint binary: $OUT_PATH"
mkdir -p "$(dirname "$OUT_PATH")"
(
  cd "$WORK_DIR"
  GOWORK=off go build -o "$OUT_PATH" ./cmd/wukongim
)

echo "gofail binary built: $OUT_PATH"
if [[ "$KEEP_WORK" -eq 1 ]]; then
  echo "temporary source retained: $WORK_DIR"
fi
