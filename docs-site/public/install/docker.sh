#!/bin/sh

set -eu

repository='ghcr.io/wukongim/wukongim'
version=${WK_VERSION:-3.0.0-beta.6}
container='wukongim'
volume='wukongim-data'
install_dir=${WK_DOCKER_INSTALL_DIR:-"$PWD/wukongim-docker"}
env_file="$install_dir/.env"
public_host=${WK_PUBLIC_HOST:-127.0.0.1}
installer_label='docs-one-click-v1'

fail() {
  printf 'WuKongIM installer: %s\n' "$1" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

random_hex() {
  od -An -N "$1" -tx1 /dev/urandom | tr -d ' \n'
}

wait_until_ready() {
  attempt=0
  while [ "$attempt" -lt 60 ]; do
    if curl --fail --silent http://127.0.0.1:5001/readyz >/dev/null 2>&1; then
      return 0
    fi
    status=$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$container" 2>/dev/null || true)
    case "$status" in
      exited|dead)
        break
        ;;
    esac
    attempt=$((attempt + 1))
    sleep 2
  done
  docker logs --tail 100 "$container" >&2 || true
  fail 'the container did not become healthy'
}

need docker
need od
need tr
need curl
docker info >/dev/null 2>&1 || fail 'the Docker daemon is not available'

case "$version" in
  v*) version=${version#v} ;;
esac

case "$version" in
  3.0.0-beta.4)
    image_digest='98a4859e057746d2f3071810ad6eebcb073e3d5fb1ccbd6a97a51ce634ed0760'
    ;;
  3.0.0-beta.5)
    image_digest='7112c059dc5517ee6370b340b4a3180c10244553e8e946f44640014c67890516'
    ;;
  3.0.0-beta.6)
    image_digest='d00b93c2d2e77bae83597eaea12191a1be88cfd458de5351e00c31ed49672786'
    ;;
  *)
    fail "unsupported WK_VERSION $version; supported versions: 3.0.0-beta.4, 3.0.0-beta.5, 3.0.0-beta.6"
    ;;
esac
image="$repository:$version@sha256:$image_digest"

case "$public_host" in
  ''|*[!A-Za-z0-9._-]*)
    fail 'WK_PUBLIC_HOST must be a hostname or IPv4 address without a URL scheme'
    ;;
esac

mkdir -p "$install_dir"

if [ ! -f "$env_file" ]; then
  cluster_suffix=$(random_hex 8)
  cluster_token=$(random_hex 32)
  manager_secret=$(random_hex 32)
  manager_password=$(random_hex 16)

  (
    umask 077
    set -C
    {
      printf '# Manager username: admin\n'
      printf '# Manager password: %s\n' "$manager_password"
      printf 'WK_NODE_ID=1\n'
      printf 'WK_NODE_DATA_DIR=/var/lib/wukongim\n'
      printf 'WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000\n'
      printf 'WK_CLUSTER_ID=wukongim-%s\n' "$cluster_suffix"
      printf 'WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]\n'
      printf 'WK_CLUSTER_JOIN_TOKEN=%s\n' "$cluster_token"
      printf 'WK_API_LISTEN_ADDR=0.0.0.0:5001\n'
      printf 'WK_EXTERNAL_TCPADDR=%s:5100\n' "$public_host"
      printf 'WK_EXTERNAL_WSADDR=ws://%s:5200\n' "$public_host"
      printf 'WK_MANAGER_LISTEN_ADDR=0.0.0.0:5301\n'
      printf 'WK_MANAGER_AUTH_ON=true\n'
      printf 'WK_MANAGER_JWT_SECRET=%s\n' "$manager_secret"
      printf 'WK_MANAGER_USERS=[{"username":"admin","password":"%s","permissions":[{"resource":"*","actions":["*"]}]}]\n' "$manager_password"
      printf 'WK_BENCH_API_ENABLE=false\n'
      printf 'WK_DEBUG_API_ENABLE=false\n'
      printf 'WK_LOG_DIR=/var/lib/wukongim/logs\n'
      printf 'WK_PROMETHEUS_DATA_DIR=/var/lib/wukongim/prometheus\n'
      printf 'WK_PLUGIN_DIR=/var/lib/wukongim/plugins\n'
      printf 'WK_PLUGIN_SOCKET_PATH=/run/wukongim/plugin.sock\n'
      printf 'WK_PLUGIN_SANDBOX_DIR=/var/lib/wukongim/plugin-sandbox\n'
      printf 'WK_PLUGIN_STATE_DIR=/var/lib/wukongim/plugin-state\n'
    } >"$env_file"
  ) || fail "cannot create $env_file"
fi

if docker container inspect "$container" >/dev/null 2>&1; then
  installed_by=$(docker inspect --format '{{index .Config.Labels "com.wukongim.installer"}}' "$container" 2>/dev/null || true)
  [ "$installed_by" = "$installer_label" ] || fail "container $container already exists and is not managed by this installer"
  installed_version=$(docker inspect --format '{{index .Config.Labels "com.wukongim.version"}}' "$container" 2>/dev/null || true)
  if [ -z "$installed_version" ]; then
    installed_image=$(docker inspect --format '{{.Config.Image}}' "$container")
    case "$installed_image" in
      "$repository:$version"@sha256:*) installed_version=$version ;;
    esac
  fi
  [ "$installed_version" = "$version" ] || fail "container $container already uses version ${installed_version:-unknown}; remove the container before installing version $version (the $volume volume is preserved)"
  running=$(docker inspect --format '{{.State.Running}}' "$container")
  if [ "$running" != 'true' ]; then
    docker start "$container" >/dev/null
  fi
else
  if ! docker run -d \
    --name "$container" \
    --label "com.wukongim.installer=$installer_label" \
    --label "com.wukongim.version=$version" \
    --restart unless-stopped \
    --stop-timeout 30 \
    --env-file "$env_file" \
    --mount "type=volume,src=$volume,dst=/var/lib/wukongim" \
    --tmpfs /run/wukongim:rw,noexec,nosuid,uid=10001,gid=10001,mode=0750,size=16m \
    --publish 127.0.0.1:5001:5001 \
    --publish 0.0.0.0:5100:5100 \
    --publish 0.0.0.0:5200:5200 \
    --publish 127.0.0.1:5301:5301 \
    --entrypoint /usr/local/bin/wukongim \
    "$image" >/dev/null; then
    failed_label=$(docker inspect --format '{{index .Config.Labels "com.wukongim.installer"}}' "$container" 2>/dev/null || true)
    if [ "$failed_label" = "$installer_label" ]; then
      docker rm --force "$container" >/dev/null 2>&1 || true
    fi
    fail 'Docker could not start the container; check port availability and Docker logs'
  fi
fi

wait_until_ready

manager_password=$(sed -n 's/^# Manager password: //p' "$env_file")
printf '\nWuKongIM is ready.\n'
printf 'Version:  %s\n' "$version"
printf 'Manager:  http://127.0.0.1:5301\n'
printf 'Username: admin\n'
printf 'Password: %s\n' "$manager_password"
printf 'Config:   %s\n' "$env_file"
printf 'Data:     Docker volume %s\n' "$volume"
