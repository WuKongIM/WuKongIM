#!/usr/bin/env bash

set -euo pipefail

review_unshare_directory="/opt/wukongim-review-agent"
review_unshare_binary="$review_unshare_directory/unshare"
review_unshare_profile="/etc/apparmor.d/wukongim-review-agent-unshare"

cleanup_user_namespace_exception() {
  if [[ -f "$review_unshare_profile" ]]; then
    sudo apparmor_parser -R "$review_unshare_profile" >/dev/null 2>&1 || true
    sudo rm -f "$review_unshare_profile" >/dev/null 2>&1 || true
  fi
  if [[ -e "$review_unshare_binary" ]]; then
    sudo rm -f "$review_unshare_binary" >/dev/null 2>&1 || true
  fi
  if [[ -d "$review_unshare_directory" ]]; then
    sudo rmdir "$review_unshare_directory" >/dev/null 2>&1 || true
  fi
}

release_user_namespace_exception() {
  [[ -f "$review_unshare_profile" ]]
  [[ -x "$review_unshare_binary" ]]
  sudo apparmor_parser -R "$review_unshare_profile"
  sudo rm -f "$review_unshare_profile"
  sudo rm -f "$review_unshare_binary"
  sudo rmdir "$review_unshare_directory"
  [[ ! -e "$review_unshare_profile" ]]
  [[ ! -e "$review_unshare_binary" ]]
  [[ ! -e "$review_unshare_directory" ]]
  [[ "$(</proc/sys/kernel/apparmor_restrict_unprivileged_userns)" == 1 ]]
}

prepare_user_namespace() {
  command -v sudo >/dev/null
  command -v apparmor_parser >/dev/null
  [[ -x /usr/bin/unshare ]]
  [[ "$(< /proc/sys/kernel/apparmor_restrict_unprivileged_userns)" == 1 ]]

  sudo install -d -o root -g root -m 0755 "$review_unshare_directory"
  sudo install -o root -g root -m 0755 \
    /usr/bin/unshare "$review_unshare_binary"
  [[ "$(stat -c '%U:%G:%a' "$review_unshare_binary")" == root:root:755 ]]
  printf '%s\n' \
    'abi <abi/4.0>,' \
    'include <tunables/global>' \
    '' \
    "$review_unshare_binary flags=(unconfined) {" \
    '  userns,' \
    '}' \
    | sudo tee "$review_unshare_profile" >/dev/null
  sudo chmod 0644 "$review_unshare_profile"
  sudo apparmor_parser -r "$review_unshare_profile"

  [[ "$(< /proc/sys/kernel/apparmor_restrict_unprivileged_userns)" == 1 ]]
  "$review_unshare_binary" --user --map-root-user true
}

apply_network_rules() {
  local mode="$1"
  local prefix=()
  local resolvers=()
  if [[ "$mode" == namespace ]]; then
    prefix=(nsenter -t "$REVIEW_NETNS_PID" -U -m -n)
    "${prefix[@]}" ip link set lo up
    resolvers=(10.0.2.3)
  else
    prefix=(sudo)
    local resolver
    while read -r resolver; do
      [[ -n "$resolver" ]] || continue
      if [[ "$resolver" != *:* && "$resolver" == *[!0-9.]* ]]; then
        echo "invalid runner DNS resolver" >&2
        exit 1
      fi
      resolvers+=("$resolver")
    done < <(awk '/^nameserver[[:space:]]+/ { print $2 }' /etc/resolv.conf)
  fi

  # Ingress and egress each receive 1 GiB, enforcing the protected 2 GiB
  # aggregate ceiling per address family. DNS is intentionally added after
  # these jumps so resolver traffic cannot bypass the ceiling.
  "${prefix[@]}" iptables -N REVIEW_AGENT_OUT
  "${prefix[@]}" iptables -A REVIEW_AGENT_OUT \
    -m quota --quota 1073741824 -j RETURN
  "${prefix[@]}" iptables -A REVIEW_AGENT_OUT -j REJECT
  "${prefix[@]}" iptables -A OUTPUT -j REVIEW_AGENT_OUT
  "${prefix[@]}" ip6tables -N REVIEW_AGENT_OUT
  "${prefix[@]}" ip6tables -A REVIEW_AGENT_OUT \
    -m quota --quota 1073741824 -j RETURN
  "${prefix[@]}" ip6tables -A REVIEW_AGENT_OUT -j REJECT
  "${prefix[@]}" ip6tables -A OUTPUT -j REVIEW_AGENT_OUT
  "${prefix[@]}" iptables -N REVIEW_AGENT_IN
  "${prefix[@]}" iptables -A REVIEW_AGENT_IN \
    -m quota --quota 1073741824 -j RETURN
  "${prefix[@]}" iptables -A REVIEW_AGENT_IN -j REJECT
  "${prefix[@]}" ip6tables -N REVIEW_AGENT_IN
  "${prefix[@]}" ip6tables -A REVIEW_AGENT_IN \
    -m quota --quota 1073741824 -j RETURN
  "${prefix[@]}" ip6tables -A REVIEW_AGENT_IN -j REJECT

  for resolver in "${resolvers[@]}"; do
    if [[ "$resolver" == *:* ]]; then
      "${prefix[@]}" ip6tables -A OUTPUT -p udp -d "$resolver" \
        --dport 53 -j ACCEPT
      "${prefix[@]}" ip6tables -A OUTPUT -p tcp -d "$resolver" \
        --dport 53 -j ACCEPT
    else
      "${prefix[@]}" iptables -A OUTPUT -p udp -d "$resolver" \
        --dport 53 -j ACCEPT
      "${prefix[@]}" iptables -A OUTPUT -p tcp -d "$resolver" \
        --dport 53 -j ACCEPT
    fi
  done

  local ipv4=(
    0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 169.254.0.0/16
    172.16.0.0/12 192.0.0.0/24 192.168.0.0/16 198.18.0.0/15
    224.0.0.0/4
  )
  local ipv6=(::/128 fc00::/7 fe80::/10 ff00::/8)
  if [[ "$mode" == host ]]; then
    ipv4+=(127.0.0.0/8)
    ipv6+=(::1/128)
  elif [[ "$mode" == namespace || "$mode" == model ]]; then
    # The namespace needs local process communication. The model host needs
    # the Codex Action's loopback model proxy; its permission profile still
    # denies localhost/private destinations to model-initiated requests.
    "${prefix[@]}" iptables -A INPUT -i lo -j ACCEPT
    "${prefix[@]}" ip6tables -A INPUT -i lo -j ACCEPT
  fi
  local cidr
  for cidr in "${ipv4[@]}"; do
    "${prefix[@]}" iptables -A OUTPUT -d "$cidr" -j REJECT
  done
  for cidr in "${ipv6[@]}"; do
    "${prefix[@]}" ip6tables -A OUTPUT -d "$cidr" -j REJECT
  done

  if [[ -n "${ORG_BLOCKED_CIDRS:-}" ]]; then
    jq -er '.[]' <<<"$ORG_BLOCKED_CIDRS" | while IFS= read -r cidr; do
      if [[ -z "$cidr" || "$cidr" == *[!0-9a-fA-F:./]* ]]; then
        echo "invalid organization-blocked CIDR" >&2
        exit 1
      fi
      if [[ "$cidr" == *:* ]]; then
        "${prefix[@]}" ip6tables -A OUTPUT -d "$cidr" -j REJECT
      else
        "${prefix[@]}" iptables -A OUTPUT -d "$cidr" -j REJECT
      fi
    done
  fi

  "${prefix[@]}" iptables -A OUTPUT -p tcp --syn -m connlimit \
    --connlimit-above 128 --connlimit-mask 0 -j REJECT
  "${prefix[@]}" ip6tables -A OUTPUT -p tcp --syn -m connlimit \
    --connlimit-above 128 --connlimit-mask 0 -j REJECT
  "${prefix[@]}" iptables -A OUTPUT -j ACCEPT
  "${prefix[@]}" ip6tables -A OUTPUT -j ACCEPT
  "${prefix[@]}" iptables -A INPUT -j REVIEW_AGENT_IN
  "${prefix[@]}" ip6tables -A INPUT -j REVIEW_AGENT_IN
  "${prefix[@]}" iptables -A INPUT -m conntrack \
    --ctstate ESTABLISHED,RELATED -j ACCEPT
  "${prefix[@]}" iptables -A INPUT -j REJECT
  "${prefix[@]}" ip6tables -A INPUT -m conntrack \
    --ctstate ESTABLISHED,RELATED -j ACCEPT
  "${prefix[@]}" ip6tables -A INPUT -j REJECT
}

start_namespace() {
  local pid_file="$1"
  [[ "$pid_file" == "$RUNNER_TEMP/"* ]]
  [[ -x "$review_unshare_binary" ]]
  command -v nsenter >/dev/null
  command -v slirp4netns >/dev/null
  command -v setpriv >/dev/null
  command -v mount >/dev/null

  local resolv_file="$RUNNER_TEMP/review-agent-resolv.conf"
  printf 'nameserver 10.0.2.3\noptions timeout:2 attempts:2\n' >"$resolv_file"
  "$review_unshare_binary" --user --map-root-user --net --mount \
    bash -c \
      'mount --make-rprivate /; mount --bind "$1" /etc/resolv.conf; exec sleep 86400' \
      review-agent-netns "$resolv_file" &
  REVIEW_NETNS_PID="$!"
  printf '%s\n' "$REVIEW_NETNS_PID" >"$pid_file"
  slirp4netns --configure --disable-host-loopback \
    "$REVIEW_NETNS_PID" tap0 \
    >"$RUNNER_TEMP/review-agent-slirp.log" 2>&1 &
  local slirp_pid="$!"
  printf '%s\n' "$slirp_pid" >"$pid_file.slirp"

  for _ in {1..50}; do
    if nsenter -t "$REVIEW_NETNS_PID" -U -m -n \
      ip link show tap0 >/dev/null 2>&1; then
      apply_network_rules namespace
      return 0
    fi
    sleep 0.1
  done
  echo "Review network namespace did not become ready" >&2
  return 1
}

join_namespace() {
  local pid_file="$1"
  shift
  [[ "$pid_file" == "$RUNNER_TEMP/"* && $# -gt 0 ]]
  local pid
  pid="$(<"$pid_file")"
  [[ "$pid" =~ ^[1-9][0-9]{0,9}$ && -d "/proc/$pid" ]]
  ulimit -n 256
  ulimit -u 512
  ulimit -t 3600
  ulimit -v 8388608
  # The trusted verifier retains namespace-only mount capability so bubblewrap
  # can create a read-only filesystem view for each untrusted child command.
  # Bubblewrap drops that capability before candidate code starts.
  exec nsenter -t "$pid" -U -m -n "$@"
}

limit_runner_worker() {
  local pid="$$"
  while [[ "$pid" =~ ^[1-9][0-9]{0,9}$ && "$pid" -gt 1 ]]; do
    local command_name
    command_name="$(<"/proc/$pid/comm")"
    if [[ "$command_name" == Runner.Worker ]]; then
      sudo prlimit --pid "$pid" \
        --cpu=3600:3600 \
        --as=8589934592:8589934592 \
        --nproc=512:512
      return 0
    fi
    pid="$(awk '/^PPid:/ { print $2 }' "/proc/$pid/status")"
  done
  echo "GitHub Runner.Worker ancestor is unavailable" >&2
  return 1
}

case "${1:-}" in
  prepare-userns)
    [[ $# -eq 1 ]]
    trap cleanup_user_namespace_exception EXIT
    prepare_user_namespace
    trap - EXIT
    ;;
  start)
    [[ $# -eq 2 ]]
    trap cleanup_user_namespace_exception EXIT
    start_namespace "$2"
    release_user_namespace_exception
    trap - EXIT
    ;;
  join)
    [[ $# -ge 3 ]]
    shift
    join_namespace "$@"
    ;;
  host)
    [[ $# -eq 2 &&
      ( "$2" == keep-sudo || "$2" == disable-sudo ) ]]
    REVIEW_NETNS_PID=""
    apply_network_rules host
    sudo chmod 000 /var/run/docker.sock 2>/dev/null || true
    if [[ "$2" == disable-sudo ]]; then
      sudo_binary="$(command -v sudo)"
      sudo chmod 000 "$sudo_binary"
    fi
    ;;
  model-host)
    [[ $# -eq 1 ]]
    REVIEW_NETNS_PID=""
    apply_network_rules model
    sudo chmod 000 /var/run/docker.sock 2>/dev/null || true
    limit_runner_worker
    ;;
  *)
    echo "usage: network-fence.sh prepare-userns | start PID_FILE | join PID_FILE COMMAND... | host keep-sudo|disable-sudo | model-host" >&2
    exit 2
    ;;
esac
