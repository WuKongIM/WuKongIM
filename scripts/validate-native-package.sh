#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repository_root"

if ! command -v goreleaser >/dev/null 2>&1; then
  echo "goreleaser is required (validated with v2.18.0)" >&2
  exit 1
fi

goreleaser check --config .goreleaser.packages.yaml
if [[ "${WK_NATIVE_PACKAGE_SKIP_BUILD:-0}" != "1" ]]; then
  goreleaser release --snapshot --clean --config .goreleaser.packages.yaml
fi

shopt -s nullglob
deb_packages=(dist/wukongim*.deb)
rpm_packages=(dist/wukongim*.rpm)
[[ "${#deb_packages[@]}" -eq 1 ]]
[[ "${#rpm_packages[@]}" -eq 1 ]]

if command -v sha256sum >/dev/null 2>&1; then
  (cd dist && sha256sum --check checksums.txt)
fi

if command -v dpkg-deb >/dev/null 2>&1; then
  dpkg-deb --contents "${deb_packages[0]}" | grep -Fq './usr/bin/wukongim'
  dpkg-deb --contents "${deb_packages[0]}" | grep -Fq './usr/lib/systemd/system/wukongim.service'
elif [[ "${WK_NATIVE_PACKAGE_REQUIRE_INSPECTORS:-0}" == "1" ]]; then
  echo "dpkg-deb is required" >&2
  exit 1
fi

if command -v rpm >/dev/null 2>&1; then
  rpm -qpl "${rpm_packages[0]}" | grep -Fq '/usr/bin/wukongim'
  rpm -qpl "${rpm_packages[0]}" | grep -Fq '/usr/lib/systemd/system/wukongim.service'
elif [[ "${WK_NATIVE_PACKAGE_REQUIRE_INSPECTORS:-0}" == "1" ]]; then
  echo "rpm is required" >&2
  exit 1
fi

echo "native package artifacts validated"
