#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 --source-sha SHA --output-dir DIRECTORY" >&2
}

source_sha=''
output_dir=''
while (( $# > 0 )); do
  case "$1" in
    --source-sha) source_sha="${2:-}"; shift 2 ;;
    --output-dir) output_dir="${2:-}"; shift 2 ;;
    *) usage; exit 2 ;;
  esac
done
[[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || { usage; exit 2; }
[[ "$output_dir" == /* && "$output_dir" != / ]] || {
  echo 'output directory must be an absolute non-root path' >&2
  exit 2
}

for tool in git go bun yarn curl tar sha256sum jq; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "required local bundle tool is unavailable: $tool" >&2
    exit 1
  }
done
[[ "$(bun --version)" == 1.3.11 ]] || { echo 'bun 1.3.11 is required' >&2; exit 1; }
[[ "$(yarn --version)" == 1.22.22 ]] || { echo 'yarn 1.22.22 is required' >&2; exit 1; }

repository_root="$(git rev-parse --show-toplevel)"
git -C "$repository_root" cat-file -e "${source_sha}^{commit}"
install -d -m 0700 "$output_dir"
[[ ! -e "$output_dir/cloud-deployment-bundle.tar.gz" ]] || {
  echo 'refusing to overwrite an existing local bundle' >&2
  exit 1
}

temporary="$(mktemp -d "${TMPDIR:-/tmp}/wukongim-local-bundle.XXXXXX")"
cleanup() {
  [[ "$temporary" == "${TMPDIR:-/tmp}"/wukongim-local-bundle.* && -d "$temporary" ]] || return 0
  rm -rf -- "$temporary"
}
trap cleanup EXIT
source_tree="$temporary/source"
bundle="$temporary/bundle"
downloads="$temporary/downloads"

git clone --shared --no-checkout "$repository_root" "$source_tree" >/dev/null
git -C "$source_tree" checkout --detach "$source_sha" >/dev/null
[[ "$(git -C "$source_tree" rev-parse HEAD)" == "$source_sha" ]]

(cd "$source_tree/web" && bun install --frozen-lockfile && bun run build)
(cd "$source_tree/demo/chatdemo" && yarn install --frozen-lockfile && yarn run build)
test -s "$source_tree/internal/access/manager/webui/dist/index.html"
test -s "$source_tree/internal/access/api/demoui/dist/index.html"

install -d -m 0755 "$bundle/bin" "$bundle/assets/manager" "$bundle/assets/demo" "$bundle/config"
for target in wukongim:./cmd/wukongim wkbench:./cmd/wkbench wkanalysis:./cmd/wkanalysis \
  wkcloudbundle:./cmd/wkcloudbundle wkcloudgate:./cmd/wkcloudgate wkcloudhost:./cmd/wkcloudhost; do
  name="${target%%:*}"
  package="${target#*:}"
  (cd "$source_tree" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags='-s -w -buildid=' -o "$bundle/bin/$name" "$package")
done
(cd "$source_tree" && GOWORK=off go build -trimpath -o "$temporary/wkcloudbundle-host" ./cmd/wkcloudbundle)
cp -a "$source_tree/internal/access/manager/webui/dist/." "$bundle/assets/manager/"
cp -a "$source_tree/internal/access/api/demoui/dist/." "$bundle/assets/demo/"
install -m 0644 "$source_tree/configs/wkbench/chat-lifecycle/formal.yaml" "$bundle/config/chat-lifecycle.yaml"
install -m 0644 "$source_tree/configs/wkbench/chat-lifecycle/rehearsal.yaml" "$bundle/config/chat-lifecycle-rehearsal.yaml"

# shellcheck disable=SC1091
source "$source_tree/.github/cloud-deployment/toolchain.env"
install -d -m 0755 "$downloads/prometheus" "$downloads/exporter" "$downloads/caddy"
prometheus_archive="prometheus-${PROMETHEUS_VERSION}.linux-amd64.tar.gz"
exporter_archive="node_exporter-${NODE_EXPORTER_VERSION}.linux-amd64.tar.gz"
caddy_archive="caddy_${CADDY_VERSION}_linux_amd64.tar.gz"
curl --fail --location --retry 3 --proto '=https' --tlsv1.2 -o "$downloads/$prometheus_archive" \
  "https://github.com/prometheus/prometheus/releases/download/v${PROMETHEUS_VERSION}/${prometheus_archive}"
curl --fail --location --retry 3 --proto '=https' --tlsv1.2 -o "$downloads/$exporter_archive" \
  "https://github.com/prometheus/node_exporter/releases/download/v${NODE_EXPORTER_VERSION}/${exporter_archive}"
curl --fail --location --retry 3 --proto '=https' --tlsv1.2 -o "$downloads/$caddy_archive" \
  "https://github.com/caddyserver/caddy/releases/download/v${CADDY_VERSION}/${caddy_archive}"
echo "$PROMETHEUS_LINUX_AMD64_SHA256  $downloads/$prometheus_archive" | sha256sum -c -
echo "$NODE_EXPORTER_LINUX_AMD64_SHA256  $downloads/$exporter_archive" | sha256sum -c -
echo "$CADDY_LINUX_AMD64_SHA256  $downloads/$caddy_archive" | sha256sum -c -
tar -xzf "$downloads/$prometheus_archive" -C "$downloads/prometheus" --strip-components=1
tar -xzf "$downloads/$exporter_archive" -C "$downloads/exporter" --strip-components=1
tar -xzf "$downloads/$caddy_archive" -C "$downloads/caddy"
install -m 0755 "$downloads/prometheus/prometheus" "$bundle/bin/prometheus"
install -m 0755 "$downloads/exporter/node_exporter" "$bundle/bin/node_exporter"
install -m 0755 "$downloads/caddy/caddy" "$bundle/bin/caddy"

"$temporary/wkcloudbundle-host" seal-offline --root "$bundle" --source-sha "$source_sha" --control-sha "$source_sha" \
  >"$temporary/bundle-manifest-output.json"
bundle_digest="$(jq -er .bundle_digest "$temporary/bundle-manifest-output.json")"
[[ "$bundle_digest" == "$("$temporary/wkcloudbundle-host" verify-offline --root "$bundle" | jq -er .bundle_digest)" ]]

COPYFILE_DISABLE=1 tar -czf "$temporary/cloud-deployment-bundle.tar.gz" -C "$bundle" .
archive_sha256="$(sha256sum "$temporary/cloud-deployment-bundle.tar.gz" | awk '{print $1}')"
[[ "$archive_sha256" =~ ^[0-9a-f]{64}$ ]]
printf '%s  %s\n' "$archive_sha256" cloud-deployment-bundle.tar.gz \
  >"$temporary/cloud-deployment-bundle.tar.gz.sha256"
install -m 0600 "$temporary/cloud-deployment-bundle.tar.gz" "$output_dir/cloud-deployment-bundle.tar.gz"
install -m 0600 "$temporary/cloud-deployment-bundle.tar.gz.sha256" "$output_dir/cloud-deployment-bundle.tar.gz.sha256"
install -m 0600 "$temporary/bundle-manifest-output.json" "$output_dir/bundle-manifest-output.json"
jq -n --arg schema 'wukongim.cloud_deployment.local_bundle/v1' --arg source_sha "$source_sha" \
  --arg bundle_digest "$bundle_digest" --arg archive "$output_dir/cloud-deployment-bundle.tar.gz" \
  '{schema:$schema,source_sha:$source_sha,bundle_digest:$bundle_digest,archive:$archive}'
