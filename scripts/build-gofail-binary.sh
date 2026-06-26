#!/usr/bin/env bash
set -euo pipefail

GOFAIL_VERSION="go.etcd.io/gofail@v0.2.0"
CMD_PACKAGE="./cmd/wukongim"
OUT_PATH=""
WORK_DIR=""
KEEP_WORK=0
DRY_RUN=0
FAILPOINT_PACKAGES=("pkg/transport")

usage() {
  cat <<'USAGE'
Usage: scripts/build-gofail-binary.sh [options]

Builds a failpoint-enabled cmd/wukongim binary from a temporary source copy.

Options:
  --out PATH              Output binary path. Defaults to /tmp/wukongim-gofail-<pid>.
  --work-dir DIR          Use DIR as the temporary source copy. Must not already exist.
  --cmd PACKAGE           Go command package to build. Defaults to ./cmd/wukongim.
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
    --cmd)
      CMD_PACKAGE="${2:?missing value for --cmd}"
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
  echo "command_package=$CMD_PACKAGE"
  echo "failpoint_packages=$(join_by_space "${FAILPOINT_PACKAGES[@]}")"
  echo "copy_manifest_cmd=git ls-files -z --cached --others --exclude-standard"
  echo "copy_cmd=tar --null -T - -cf -"
  echo "enable_cmd=gofail enable $(join_by_space "${FAILPOINT_PACKAGES[@]}")"
  echo "package_name_cmd=GOWORK=off go list -f '{{.Name}}'"
  echo "postprocess_cmd=scripts/gofail-postprocess-generated.sh"
  echo "runtime_dep_cmd=GOWORK=off go get $GOFAIL_RUNTIME_DEP"
  echo "build_cmd=GOWORK=off go build -o $OUT_PATH $CMD_PACKAGE"
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

echo "copying source to $WORK_DIR"
(
  cd "$repo_root"
  git ls-files -z --cached --others --exclude-standard | LC_ALL=C tar --null -T - -cf -
) | (
  cd "$WORK_DIR"
  LC_ALL=C tar -xf -
)

gofail_bin_dir="$(mktemp -d "${TMPDIR:-/tmp}/wukongim-gofail-bin.XXXXXX")"
trap 'rm -rf "$gofail_bin_dir"; [[ "$KEEP_WORK" -eq 0 ]] && rm -rf "$WORK_DIR"' EXIT

echo "installing $GOFAIL_VERSION"
GOWORK=off GOBIN="$gofail_bin_dir" go install "$GOFAIL_VERSION"

package_specs=()
for failpoint_package in "${FAILPOINT_PACKAGES[@]}"; do
  package_path="${failpoint_package#./}"
  package_name="$(
    cd "$WORK_DIR"
    GOWORK=off go list -f '{{.Name}}' "./$package_path"
  )"
  package_specs+=("$package_path:$package_name")
done

echo "enabling failpoints: $(join_by_space "${FAILPOINT_PACKAGES[@]}")"
(
  cd "$WORK_DIR"
  PATH="$gofail_bin_dir:$PATH" gofail enable "${FAILPOINT_PACKAGES[@]}"
)

echo "postprocessing generated failpoint packages"
bash "$WORK_DIR/scripts/gofail-postprocess-generated.sh" "$WORK_DIR" "${package_specs[@]}"

echo "pinning failpoint runtime: $GOFAIL_RUNTIME_DEP"
(
  cd "$WORK_DIR"
  GOWORK=off go get "$GOFAIL_RUNTIME_DEP"
)

echo "building failpoint binary: $OUT_PATH"
mkdir -p "$(dirname "$OUT_PATH")"
(
  cd "$WORK_DIR"
  GOWORK=off go build -o "$OUT_PATH" "$CMD_PACKAGE"
)

echo "gofail binary built: $OUT_PATH"
if [[ "$KEEP_WORK" -eq 1 ]]; then
  echo "temporary source retained: $WORK_DIR"
fi
