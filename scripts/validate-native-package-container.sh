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

case "$format" in
  deb)
    package="$(find "$package_dir" -maxdepth 1 -type f -name 'wukongim*.deb' -print -quit)"
    install_command='apt-get -o Acquire::Retries=3 update && DEBIAN_FRONTEND=noninteractive apt-get -o Acquire::Retries=3 install -y /tmp/wukongim.deb'
    upgrade_command='DEBIAN_FRONTEND=noninteractive apt-get install --reinstall -y /tmp/wukongim.deb'
    remove_command='DEBIAN_FRONTEND=noninteractive apt-get remove -y wukongim'
    mount_target='/tmp/wukongim.deb'
    ;;
  rpm)
    package="$(find "$package_dir" -maxdepth 1 -type f -name 'wukongim*.rpm' -print -quit)"
    install_command='dnf install -y /tmp/wukongim.rpm'
    upgrade_command='dnf reinstall -y /tmp/wukongim.rpm'
    remove_command='dnf remove -y wukongim'
    mount_target='/tmp/wukongim.rpm'
    ;;
  *)
    echo "unsupported package format: $format" >&2
    exit 64
    ;;
esac

[[ -n "$package" ]]

wrapper_dir="$(mktemp -d)"
cleanup() { find "$wrapper_dir" -depth -delete; }
trap cleanup EXIT HUP INT TERM
cat >"$wrapper_dir/systemctl" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>/tmp/wukongim-systemctl.calls
exit 0
EOF
chmod 0755 "$wrapper_dir/systemctl"

docker run --rm \
  --platform linux/amd64 \
  --env "NATIVE_PACKAGE_TEST_APT_MIRROR=${WK_NATIVE_PACKAGE_APT_MIRROR:-}" \
  --volume "$package:$mount_target:ro" \
  --volume "$wrapper_dir/systemctl:/tmp/wukongim-systemctl-wrapper:ro" \
  "$image" \
  /bin/sh -euxc "
    : >/tmp/wukongim-systemctl.calls
    if [ -n \"\$NATIVE_PACKAGE_TEST_APT_MIRROR\" ] && \
      [ -f /etc/apt/sources.list.d/ubuntu.sources ]; then
      sed -i \
        -e \"s#http://archive.ubuntu.com/ubuntu/#\$NATIVE_PACKAGE_TEST_APT_MIRROR/#\" \
        -e \"s#http://security.ubuntu.com/ubuntu/#\$NATIVE_PACKAGE_TEST_APT_MIRROR/#\" \
        /etc/apt/sources.list.d/ubuntu.sources
    fi
    $install_command
    test -x /usr/bin/wukongim
    test -f /usr/lib/systemd/system/wukongim.service
    getent passwd wukongim
    systemd-analyze verify /usr/lib/systemd/system/wukongim.service
    test -x /usr/bin/systemctl
    mv /usr/bin/systemctl /usr/bin/systemctl.real
    cp /tmp/wukongim-systemctl-wrapper /usr/bin/systemctl
    chmod 0755 /usr/bin/systemctl
    /usr/bin/wukongim version --output json | grep -F '\"build_source\":\"release\"'
    test ! -e /etc/wukongim/wukongim.toml
    test ! -e /etc/systemd/system/multi-user.target.wants/wukongim.service
    printf '%s\n' 'native-package-container-test' | /usr/bin/wukongim config init \
      --config /etc/wukongim/wukongim.toml \
      --admin-password-stdin
    /usr/bin/wukongim config validate --config /etc/wukongim/wukongim.toml
    test \"\$(stat -c '%a %U %G' /etc/wukongim/wukongim.toml)\" = '640 root wukongim'
    printf '%s\n' 'package-upgrade-state' >/var/lib/wukongim/package-upgrade-sentinel
    (cd / && sha256sum \
      etc/wukongim/wukongim.toml \
      var/lib/wukongim/package-upgrade-sentinel) \
      >/tmp/wukongim-package-state.sha256
    : >/tmp/wukongim-systemctl.calls
    $upgrade_command
    test -s /tmp/wukongim-systemctl.calls
    if grep -Eq '(^|[[:space:]])(start|stop|restart|enable|disable|try-restart|reload-or-restart|preset)([[:space:]]|$)' \
      /tmp/wukongim-systemctl.calls; then
      cat /tmp/wukongim-systemctl.calls >&2
      echo 'native package upgrade changed service activation state' >&2
      exit 1
    fi
    if grep -Fvx 'daemon-reload' /tmp/wukongim-systemctl.calls; then
      cat /tmp/wukongim-systemctl.calls >&2
      echo 'native package upgrade made an unexpected systemctl call' >&2
      exit 1
    fi
    (cd / && sha256sum --check /tmp/wukongim-package-state.sha256)
    test ! -e /etc/systemd/system/multi-user.target.wants/wukongim.service
    /usr/bin/wukongim config validate --config /etc/wukongim/wukongim.toml
    $remove_command
    test -d /var/lib/wukongim
    test -f /etc/wukongim/wukongim.toml
    test -f /var/lib/wukongim/package-upgrade-sentinel
  "
