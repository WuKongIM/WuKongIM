#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 2 ]]; then
  echo "usage: $0 <container-image> <deb|rpm>" >&2
  exit 64
fi

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
package_dir="${WK_NATIVE_PACKAGE_DIST_DIR:-$repository_root/dist}"
[[ -d "$package_dir" && ! -L "$package_dir" ]] || {
  echo "native package directory is missing or unsafe: $package_dir" >&2
  exit 66
}
package_dir="$(cd "$package_dir" && pwd -P)"
image="$1"
format="$2"

case "$image|$format" in
  ubuntu:24.04\|deb|debian:12\|deb|rockylinux:9\|rpm|almalinux:9\|rpm) ;;
  *)
    echo "unsupported native package lifecycle target: $image $format" >&2
    exit 64
    ;;
esac

shopt -s nullglob
case "$format" in
  deb)
    packages=("$package_dir"/wukongim*.deb)
    mount_target="/opt/wukongim-package.deb"
    install_command='apt-get -o Acquire::Retries=3 update && DEBIAN_FRONTEND=noninteractive apt-get -o Acquire::Retries=3 install --no-install-recommends -y /opt/wukongim-package.deb'
    reinstall_command='DEBIAN_FRONTEND=noninteractive apt-get -o Acquire::Retries=3 install --no-install-recommends --reinstall -y /opt/wukongim-package.deb'
    remove_command='DEBIAN_FRONTEND=noninteractive apt-get remove -y wukongim'
    ;;
  rpm)
    packages=("$package_dir"/wukongim*.rpm)
    mount_target="/opt/wukongim-package.rpm"
    install_command='dnf install -y /opt/wukongim-package.rpm'
    reinstall_command='dnf reinstall -y /opt/wukongim-package.rpm'
    remove_command='dnf remove -y wukongim'
    ;;
esac
[[ "${#packages[@]}" -eq 1 ]] || {
  echo "expected exactly one $format package in $package_dir, found ${#packages[@]}" >&2
  exit 65
}
package="${packages[0]}"

container_name="wk-native-lifecycle-${format}-$$-${RANDOM}"
[[ "$container_name" =~ ^wk-native-lifecycle-(deb|rpm)-[0-9]+-[0-9]+$ ]]
active_command_pid=0
active_watchdog_pid=0
active_timeout_marker=""
total_watchdog_pid=0
total_timeout_marker=""

run_bounded() {
  local timeout_seconds="$1"
  shift
  local command_pid marker watchdog_pid status

  "$@" &
  command_pid=$!
  active_command_pid="$command_pid"
  marker="$(mktemp "${TMPDIR:-/tmp}/wk-native-lifecycle-timeout.XXXXXX")"
  active_timeout_marker="$marker"
  (
    timer_pid=0
    trap 'if [[ "$timer_pid" =~ ^[1-9][0-9]*$ ]]; then kill -TERM "$timer_pid" >/dev/null 2>&1 || true; fi; exit 0' TERM INT
    sleep "$timeout_seconds" &
    timer_pid=$!
    wait "$timer_pid" || exit 0
    if [[ -e "$marker" ]] && kill -0 "$command_pid" >/dev/null 2>&1; then
      echo "command timed out after ${timeout_seconds}s: $1" >&2
      kill -TERM "$command_pid" >/dev/null 2>&1 || true
      sleep 5
      kill -KILL "$command_pid" >/dev/null 2>&1 || true
    fi
    rm -f "$marker"
  ) &
  watchdog_pid=$!
  active_watchdog_pid="$watchdog_pid"

  if wait "$command_pid"; then
    status=0
  else
    status=$?
  fi
  rm -f "$marker"
  kill -TERM "$watchdog_pid" >/dev/null 2>&1 || true
  active_command_pid=0
  active_watchdog_pid=0
  active_timeout_marker=""
  return "$status"
}

cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  if [[ -n "$active_timeout_marker" ]]; then
    rm -f "$active_timeout_marker"
  fi
  if [[ -n "$total_timeout_marker" ]]; then
    rm -f "$total_timeout_marker"
  fi
  for pid in "$active_command_pid" "$active_watchdog_pid" "$total_watchdog_pid"; do
    if [[ "$pid" =~ ^[1-9][0-9]*$ ]]; then
      kill -TERM "$pid" >/dev/null 2>&1 || true
    fi
  done
  if ((status != 0)) && run_bounded 10 docker inspect "$container_name" >/dev/null 2>&1; then
    echo "native package lifecycle container logs:" >&2
    run_bounded 15 docker logs --tail 300 "$container_name" >&2 || true
    if run_bounded 10 docker exec "$container_name" test -x /usr/bin/systemctl >/dev/null 2>&1; then
      echo "wukongim service status:" >&2
      run_bounded 15 docker exec "$container_name" systemctl status --no-pager wukongim.service >&2 || true
      echo "wukongim service journal:" >&2
      run_bounded 15 docker exec "$container_name" journalctl --no-pager -u wukongim.service -n 300 >&2 || true
    fi
  fi
  run_bounded 30 docker rm --force --volumes "$container_name" >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM

total_timeout_marker="$(mktemp "${TMPDIR:-/tmp}/wk-native-lifecycle-total-timeout.XXXXXX")"
(
  timer_pid=0
  trap 'if [[ "$timer_pid" =~ ^[1-9][0-9]*$ ]]; then kill -TERM "$timer_pid" >/dev/null 2>&1 || true; fi; exit 0' TERM INT
  sleep 900 &
  timer_pid=$!
  wait "$timer_pid" || exit 0
  if [[ -e "$total_timeout_marker" ]]; then
    echo "native package lifecycle validation exceeded its 900-second total deadline" >&2
    kill -TERM "$$" >/dev/null 2>&1 || true
    rm -f "$total_timeout_marker"
  fi
) &
total_watchdog_pid=$!

run_in_container() {
  run_in_container_bounded 30 "$@"
}

run_in_container_bounded() {
  local timeout_seconds="$1"
  shift
  run_bounded "$timeout_seconds" docker exec "$container_name" "$@"
}

run_shell() {
  run_shell_bounded 30 "$1"
}

run_shell_bounded() {
  local timeout_seconds="$1"
  shift
  run_in_container_bounded "$timeout_seconds" /bin/sh -ec "$1"
}

probe_http() {
  local path="$1"
  run_in_container_bounded 5 /bin/bash -ec "exec 3<>/dev/tcp/127.0.0.1/5001 2>/dev/null; printf 'GET $path HTTP/1.0\\r\\nHost: 127.0.0.1\\r\\nConnection: close\\r\\n\\r\\n' >&3; IFS= read -r -t 2 status <&3; [[ \"\$status\" == *' 200 '* ]]"
}

wait_for_systemd() {
  local deadline state
  deadline=$((SECONDS + 300))
  while ((SECONDS < deadline)); do
    state="$(run_in_container systemctl is-system-running 2>/dev/null || true)"
    case "$state" in
      running|degraded) return 0 ;;
    esac
    if [[ "$(run_bounded 10 docker inspect --format '{{.State.Running}}' "$container_name")" != "true" ]]; then
      echo "systemd container stopped while booting" >&2
      return 1
    fi
    sleep 1
  done
  echo "package bootstrap and systemd did not reach running or degraded state within 300 seconds" >&2
  return 1
}

wait_for_ready() {
  local deadline
  deadline=$((SECONDS + 90))
  while ((SECONDS < deadline)); do
    if probe_http /healthz && probe_http /readyz; then
      return 0
    fi
    if ! run_in_container systemctl is-active --quiet wukongim.service; then
      echo "wukongim.service stopped before readiness" >&2
      return 1
    fi
    if ((SECONDS < deadline)); then
      sleep 1
    fi
  done
  echo "wukongim.service did not become ready within 90 seconds" >&2
  return 1
}

unit_property() {
  run_in_container systemctl show wukongim.service --property="$1" --value
}

require_unit_stopped() {
  local active_state sub_state result main_pid
  active_state="$(unit_property ActiveState)"
  sub_state="$(unit_property SubState)"
  result="$(unit_property Result)"
  main_pid="$(unit_property MainPID)"
  if [[ "$active_state" != "inactive" || "$sub_state" != "dead" || "$result" != "success" || "$main_pid" != "0" ]]; then
    echo "wukongim.service did not reach a clean inactive/dead state: ActiveState=$active_state SubState=$sub_state Result=$result MainPID=$main_pid" >&2
    return 1
  fi
}

require_inactive_and_disabled() {
  local load_state unit_file_state
  load_state="$(unit_property LoadState)"
  unit_file_state="$(unit_property UnitFileState)"
  require_unit_stopped
  if [[ "$load_state" != "loaded" || "$unit_file_state" != "disabled" ]]; then
    echo "wukongim.service is not installed disabled: LoadState=$load_state UnitFileState=$unit_file_state" >&2
    return 1
  fi
}

wait_for_pid_exit() {
  local deadline pid state
  pid="$1"
  deadline=$((SECONDS + 30))
  while ((SECONDS < deadline)); do
    state="$(run_shell "if [ -e /proc/$pid ]; then printf present; else printf absent; fi")"
    if [[ "$state" == "absent" ]]; then
      return 0
    fi
    sleep 1
  done
  echo "wukongim process $pid survived package removal" >&2
  return 1
}

require_unit_removed() {
  local removed_pid load_state unit_file_state
  removed_pid="$1"
  wait_for_pid_exit "$removed_pid"
  load_state="$(unit_property LoadState)"
  unit_file_state="$(unit_property UnitFileState)"
  if [[ "$load_state" != "not-found" || -n "$unit_file_state" ]]; then
    echo "removed wukongim.service remains registered: LoadState=$load_state UnitFileState=$unit_file_state" >&2
    return 1
  fi
  run_in_container test ! -e /etc/systemd/system/multi-user.target.wants/wukongim.service
}

service_pid() {
  unit_property MainPID
}

service_invocation_id() {
  unit_property InvocationID
}

service_start_time() {
  local pid="$1"
  run_shell "awk '{print \$22}' /proc/$pid/stat"
}

require_service_identity() {
  local expected_pid="$1"
  local expected_invocation="$2"
  local expected_start_time="$3"
  local actual_pid actual_invocation actual_start_time
  actual_pid="$(service_pid)"
  actual_invocation="$(service_invocation_id)"
  actual_start_time="$(service_start_time "$actual_pid")"
  if [[ "$actual_pid" != "$expected_pid" || "$actual_invocation" != "$expected_invocation" || "$actual_start_time" != "$expected_start_time" ]]; then
    echo "package-manager reinstall replaced the live service identity" >&2
    return 1
  fi
}

state_manifest_command="cd / && {
  stat -c '%a %u %g %n' etc/wukongim etc/wukongim/wukongim.toml var/lib/wukongim var/lib/wukongim/package-lifecycle-sentinel var/log/wukongim var/log/wukongim/package-lifecycle-sentinel
  getent passwd wukongim
  getent group wukongim
}"

record_operator_state() {
  run_shell "printf '%s\\n' package-lifecycle-data >/var/lib/wukongim/package-lifecycle-sentinel && printf '%s\\n' package-lifecycle-log >/var/log/wukongim/package-lifecycle-sentinel && cd / && sha256sum etc/wukongim/wukongim.toml var/lib/wukongim/package-lifecycle-sentinel var/log/wukongim/package-lifecycle-sentinel >/tmp/wukongim-lifecycle-state.sha256 && $state_manifest_command >/tmp/wukongim-lifecycle-state.manifest && cd /tmp && sha256sum wukongim-lifecycle-state.manifest >/tmp/wukongim-lifecycle-state-manifest.sha256"
}

require_operator_state() {
  run_shell "cd / && sha256sum --check /tmp/wukongim-lifecycle-state.sha256 && $state_manifest_command >/tmp/wukongim-lifecycle-state.current && test \"\$(sha256sum /tmp/wukongim-lifecycle-state.current | cut -d' ' -f1)\" = \"\$(cut -d' ' -f1 /tmp/wukongim-lifecycle-state-manifest.sha256)\""
}

bootstrap_command="set -eu
if [ -n \"\${NATIVE_PACKAGE_TEST_APT_MIRROR:-}\" ] && [ -f /etc/apt/sources.list.d/ubuntu.sources ]; then
  sed -i -e \"s#http://archive.ubuntu.com/ubuntu/#\${NATIVE_PACKAGE_TEST_APT_MIRROR}/#\" -e \"s#http://security.ubuntu.com/ubuntu/#\${NATIVE_PACKAGE_TEST_APT_MIRROR}/#\" /etc/apt/sources.list.d/ubuntu.sources
fi
if [ -f /etc/apt/sources.list.d/ubuntu.sources ]; then
  sed -i 's/^Components:.*/Components: main/' /etc/apt/sources.list.d/ubuntu.sources
fi
$install_command
if [ -x /lib/systemd/systemd ]; then exec /lib/systemd/systemd; fi
if [ -x /usr/lib/systemd/systemd ]; then exec /usr/lib/systemd/systemd; fi
echo 'systemd executable is missing after package installation' >&2
exit 1"

run_bounded 300 docker pull --platform linux/amd64 "$image" >/dev/null
run_bounded 30 docker run --detach \
  --name "$container_name" \
  --hostname wk-native-lifecycle \
  --platform linux/amd64 \
  --pull never \
  --privileged \
  --cgroupns private \
  --stop-signal SIGRTMIN+3 \
  --tmpfs /run:rw,nosuid,nodev,mode=755 \
  --tmpfs /run/lock:rw,nosuid,nodev,mode=755 \
  --volume "$package:$mount_target:ro" \
  --env container=docker \
  --env "NATIVE_PACKAGE_TEST_APT_MIRROR=${WK_NATIVE_PACKAGE_APT_MIRROR:-}" \
  "$image" \
  /bin/sh -ec "$bootstrap_command" >/dev/null

wait_for_systemd
run_in_container test -x /usr/bin/wukongim
run_in_container test -f /usr/lib/systemd/system/wukongim.service
run_in_container systemd-analyze verify /usr/lib/systemd/system/wukongim.service
run_shell "/usr/bin/wukongim version --output json | grep -F '\"build_source\":\"release\"'"
run_in_container test ! -e /etc/wukongim/wukongim.toml
require_inactive_and_disabled

run_shell "printf '%s\\n' 'native-package-lifecycle-test' | /usr/bin/wukongim config init --config /etc/wukongim/wukongim.toml --admin-password-stdin"
run_in_container /usr/bin/wukongim config validate --config /etc/wukongim/wukongim.toml
run_shell "test \"\$(stat -c '%a %U %G' /etc/wukongim/wukongim.toml)\" = '640 root wukongim'"
record_operator_state

run_in_container systemctl enable --now wukongim.service
wait_for_ready
run_in_container systemctl is-enabled --quiet wukongim.service
first_pid="$(service_pid)"
[[ "$first_pid" =~ ^[1-9][0-9]*$ ]]
run_shell "test \"\$(awk '/^Uid:/ {print \$2}' /proc/$first_pid/status)\" = \"\$(id -u wukongim)\""
first_invocation="$(service_invocation_id)"
first_start_time="$(service_start_time "$first_pid")"
[[ "$first_invocation" =~ ^[0-9a-f]{32}$ && "$first_start_time" =~ ^[1-9][0-9]*$ ]]

run_shell_bounded 300 "$reinstall_command"
run_in_container systemctl is-active --quiet wukongim.service
run_in_container systemctl is-enabled --quiet wukongim.service
wait_for_ready
require_service_identity "$first_pid" "$first_invocation" "$first_start_time"
sleep 2
wait_for_ready
require_service_identity "$first_pid" "$first_invocation" "$first_start_time"
require_operator_state

run_in_container systemctl restart wukongim.service
wait_for_ready
second_pid="$(service_pid)"
[[ "$second_pid" =~ ^[1-9][0-9]*$ && "$second_pid" != "$first_pid" ]] || {
  echo "explicit restart did not replace the wukongim process" >&2
  exit 1
}

run_in_container systemctl stop wukongim.service
require_unit_stopped
if probe_http /readyz; then
  echo "readyz remained reachable after explicit stop" >&2
  exit 1
fi

run_in_container systemctl start wukongim.service
wait_for_ready
removed_pid="$(service_pid)"
[[ "$removed_pid" =~ ^[1-9][0-9]*$ ]]
run_shell_bounded 300 "$remove_command"
require_unit_removed "$removed_pid"
run_in_container test ! -e /usr/bin/wukongim
run_in_container test ! -e /usr/lib/systemd/system/wukongim.service
require_operator_state
run_shell_bounded 300 "$install_command"
require_inactive_and_disabled
require_operator_state
run_in_container /usr/bin/wukongim config validate --config /etc/wukongim/wukongim.toml

run_in_container systemctl enable --now wukongim.service
wait_for_ready
run_in_container systemctl stop wukongim.service
run_in_container systemctl disable wukongim.service
require_inactive_and_disabled

echo "native package lifecycle validation passed for $image $format"
