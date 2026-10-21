#!/usr/bin/env python3
"""Validate the preview repositories with clean, download-only clients.

The four client images are immutable linux/amd64 manifests.  Current snapshots
first install the reviewed, data-only repository bootstrap package, then use
its installed source/repository file and key to perform an authenticated,
download-only product transaction.  APT downloads ``wukongim`` without
installing it.  DNF runs its genuine ``install --downloadonly`` transaction.
Both clients prove that ``wukongim`` is absent before and after the transaction;
the product package is never executed in the download-only clients. An explicit
remote-only installed-CLI phase can then install the authenticated bytes in
fresh credential-free containers and run the reviewed offline acceptance fixtures.  Bootstrap packages are installed only
after their direct-download and indexed copies have been matched to the
canonical public bootstrap manifest.  The downloaded RPM is also checked
against the reviewed public certificate in an isolated RPM database.

Local validation mounts an already verified Pages site read-only and also
checks each downloaded payload against the snapshot inventory.  Remote
validation first pins the public keys at the endpoint to the reviewed local
certificates and then lets the clean clients access the HTTPS repository.  If
a reviewed snapshot is supplied in remote mode, every indexed and retained
payload is fetched without redirects and checked before the clean clients run.
"""

from __future__ import annotations

import argparse
import hashlib
import importlib.util
import json
import os
import re
import stat
import shutil
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path, PurePosixPath
from typing import Any, Callable, Sequence


SCRIPT_DIR = Path(__file__).resolve().parent
COMPOSER_PATH = SCRIPT_DIR / "compose-package-site.py"
COMPOSER_SPEC = importlib.util.spec_from_file_location(
    "_repository_entrypoint_composer", COMPOSER_PATH
)
if COMPOSER_SPEC is None or COMPOSER_SPEC.loader is None:  # pragma: no cover
    raise RuntimeError("cannot load compose-package-site.py")
COMPOSER = importlib.util.module_from_spec(COMPOSER_SPEC)
COMPOSER_SPEC.loader.exec_module(COMPOSER)

RECEIPT_SCHEMA = "wukongim/production-package-client-validation/v4"
SNAPSHOT_SCHEMA = "wukongim.native_package_snapshot/v3"
STATUS_SCHEMA = "wukongim.native_package_repository_status/v2"
BOOTSTRAP_SCHEMA = "wukongim.native_package_bootstrap_inventory/v1"
MAX_CONTROL_BYTES = 8 * 1024 * 1024
MAX_CERTIFICATE_BYTES = 1024 * 1024
MAX_BOOTSTRAP_BYTES = 16 * 1024 * 1024
MAX_REPOSITORY_ENTRYPOINT_BYTES = 64 * 1024
MAX_DOWNLOAD_BYTES = 800 * 1024 * 1024
APT_CA_BUNDLE_PATH = "/etc/ssl/certs/ca-certificates.crt"
SHA1_RE = re.compile(r"^[0-9a-f]{40}$")
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
FINGERPRINT_RE = re.compile(r"^[0-9A-F]{40}$")
OCI_DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
UTC_RE = re.compile(
    r"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$"
)
VERSION_RE = re.compile(
    r"^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)-"
    r"(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)"
    r"(?:\.(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*$"
)
RELEASE_VERSION_RE = re.compile(
    r"^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$"
)
SAFE_COMPONENT_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._+~-]{0,254}$")

LEGACY_BOOTSTRAPLESS_IDENTITY = (
    381152722,
    "637bc91bc8753a55dba0ebb346384a0a7e7387b6",
)
LEGACY_REPOSITORY_ENTRYPOINT_FREE_IDENTITIES = {
    (381713091, "46cfe4d94dc98830774918e4c68744aeb03ca926"),
}

SNAPSHOT_FIELDS = {
    "schema", "audit_release_id", "control_sha", "releases", "retirement",
    "payloads", "public_keys", "source_attestations", "toolchain",
}
RELEASE_FIELDS = {
    "version", "source_sha", "source_release_id", "package_release_id",
    "deb_sha256", "rpm_sha256", "state", "not_before",
}
PAYLOAD_FIELDS = {"version", "path", "source_sha256", "published_sha256", "indexed"}
PUBLIC_KEY_FIELDS = {
    "path", "sha256", "size", "primary_fingerprint",
    "current_signing_subkey_fingerprint", "next_signing_subkey_fingerprint",
    "historical_signing_subkey_fingerprints",
}
SOURCE_ATTESTATION_FIELDS = {"summary_sha256", "files"}
ARTIFACT_FIELDS = {"path", "sha256", "size"}
TOOLCHAIN_FIELDS = {
    "image", "digest", "workflow_sha", "manifest_sha256", "manifest_size",
}
BOOTSTRAP_FIELDS = {"schema", "version", "packages"}
BOOTSTRAP_PACKAGE_FIELDS = {
    "name", "version", "architecture", "filename", "repository_path",
    "download_path", "source_sha256", "source_size", "published_sha256",
    "published_size", "new",
}

# These are platform-specific linux/amd64 manifest digests, not mutable tag or
# multi-platform index references.  Updating one is a reviewed control change.
APT_CLIENTS = (
    (
        "ubuntu-24.04",
        "ubuntu:24.04@sha256:1e0a86e57d247923571b75e0aaf48a1449cf8c543d51fb3e07a4a7d7bfa79316",
    ),
    (
        "debian-13",
        "debian:13-slim@sha256:abc9cb88a5587630d7f915f47b23b0668fe250fbfc6457aa4d52b534c1bbf73f",
    ),
)
RPM_CLIENTS = (
    (
        "rocky-linux-9",
        # The init variant contains systemd, which satisfies the package's
        # runtime dependency while the reviewed repository remains offline.
        "quay.io/rockylinux/rockylinux@sha256:aa1303868c5fad2c780735de874d9dafefe6743b701579eb7acd9bd8bdc523b5",
    ),
    (
        "alma-linux-9",
        "almalinux:9@sha256:28db580abb508f7ccbc0ac6d53e1d8da9d42a26c77fa3dcc26ac2726673fbe3e",
    ),
)


APT_SCRIPT = r"""
set -euo pipefail
export LC_ALL=C
export DEBIAN_FRONTEND=noninteractive
mkdir -p /var/lib/apt/lists/partial /var/cache/apt/archives/partial
if dpkg-query -W -f='${db:Status-Abbrev}\n' wukongim 2>/dev/null | grep -q '^ii '; then
  echo 'wukongim was already installed in the clean APT client' >&2
  exit 1
fi
apt_source=/tmp/wukongim-preview.list
if [[ -n "${WK_BOOTSTRAP_PACKAGE:-}" ]]; then
  test -r "$WK_BOOTSTRAP_PACKAGE"
  bootstrap_control=/tmp/bootstrap-control
  mkdir "$bootstrap_control"
  dpkg-deb --control "$WK_BOOTSTRAP_PACKAGE" "$bootstrap_control"
  test "$(find "$bootstrap_control" -mindepth 1 -maxdepth 1 -type f \
    -printf '%f\n' | sort)" = $'conffiles\ncontrol'
  test -z "$(find "$bootstrap_control" -mindepth 1 -maxdepth 1 ! -type f -print -quit)"
  test "$(dpkg-deb -f "$WK_BOOTSTRAP_PACKAGE" Package)" = wukongim-archive-keyring
  test "$(dpkg-deb -f "$WK_BOOTSTRAP_PACKAGE" Architecture)" = all

  # Keep the immutable image read-only.  A private copy of its package database
  # records the real bootstrap installation while the package's two payload
  # directories are dedicated tmpfs mounts.
  cp -a /var/lib/dpkg /tmp/dpkg
  bootstrap_apt_options=(
    -o Dir::State::status=/tmp/dpkg/status
    -o DPkg::Options::=--admindir=/tmp/dpkg
    -o APT::Sandbox::User=root
  )
  apt-get "${bootstrap_apt_options[@]}" install --yes --no-install-recommends \
    "$WK_BOOTSTRAP_PACKAGE"
  dpkg-query --admindir=/tmp/dpkg -W -f='${db:Status-Abbrev}\n' \
    wukongim-archive-keyring | grep -q '^ii '

  installed_source=/etc/apt/sources.list.d/wukongim-preview.sources
  installed_key=/usr/share/keyrings/wukongim-archive-keyring.pgp
  test -f "$installed_source" && test ! -L "$installed_source"
  test -f "$installed_key" && test ! -L "$installed_key"
  test "$(stat -c '%a:%U:%G' "$installed_source")" = 644:root:root
  test "$(stat -c '%a:%U:%G' "$installed_key")" = 644:root:root
  cat >/tmp/expected-wukongim-preview.sources <<'EOF'
Types: deb
URIs: https://packages.githubim.com/apt
Suites: preview
Components: main
Architectures: amd64
Signed-By: /usr/share/keyrings/wukongim-archive-keyring.pgp
Enabled: yes
EOF
  cmp -s /tmp/expected-wukongim-preview.sources "$installed_source"

  apt_source="$installed_source"
  if ! grep -Fxq "URIs: $WK_REPOSITORY_URL" "$installed_source"; then
    apt_source=/tmp/wukongim-preview.sources
    sed "s#^URIs: https://packages.githubim.com/apt\$#URIs: $WK_REPOSITORY_URL#" \
      "$installed_source" >"$apt_source"
  fi
else
  printf 'deb [arch=amd64 signed-by=/keys/apt-preview.asc] %s preview main\n' \
    "$WK_REPOSITORY_URL" >"$apt_source"
fi
apt_options=(
  -o Dir::Etc::sourcelist="$apt_source"
  -o Dir::Etc::sourceparts=-
  -o Dir::State::lists=/var/lib/apt/lists
  -o Dir::Cache=/var/cache/apt
  -o APT::Get::List-Cleanup=0
  -o APT::Get::AllowUnauthenticated=false
  -o Acquire::AllowInsecureRepositories=false
  -o Acquire::AllowDowngradeToInsecureRepositories=false
  -o Acquire::Check-Valid-Until=true
  -o APT::Sandbox::User=root
)
if [[ -n "${WK_CA_BUNDLE:-}" ]]; then
  test -r "$WK_CA_BUNDLE"
  apt_options+=(
    -o Acquire::https::CaInfo="$WK_CA_BUNDLE"
  )
fi
apt-get "${apt_options[@]}" update
cd /downloads
apt-get "${apt_options[@]}" download wukongim
shopt -s nullglob
packages=(/downloads/*.deb)
((${#packages[@]} == 1))
if dpkg-query -W -f='${db:Status-Abbrev}\n' wukongim 2>/dev/null | grep -q '^ii '; then
  echo 'APT download-only validation installed wukongim' >&2
  exit 1
fi
if [[ -n "${WK_BOOTSTRAP_PACKAGE:-}" ]]; then
  dpkg-query --admindir=/tmp/dpkg -W -f='${db:Status-Abbrev}\n' \
    wukongim-archive-keyring | grep -q '^ii '
  if dpkg-query --admindir=/tmp/dpkg -W -f='${db:Status-Abbrev}\n' \
    wukongim 2>/dev/null | grep -q '^ii '; then
    echo 'APT download-only validation installed wukongim in the bootstrap database' >&2
    exit 1
  fi
fi
""".strip()


RPM_SCRIPT = r"""
set -euo pipefail
export LC_ALL=C
client_root=/tmp/client-root
mkdir -p \
  /tmp/repos.d /tmp/dnf-cache /tmp/dnf-state /tmp/rpmdb /tmp/bootstrap-rpmdb \
  "$client_root/var/lib/rpm"
cp -a /var/lib/rpm/. "$client_root/var/lib/rpm/"
if rpm --root "$client_root" -q wukongim >/dev/null 2>&1; then
  echo 'wukongim was already installed in the clean RPM client' >&2
  exit 1
fi
rpm --root "$client_root" -q systemd >/dev/null
if [[ -n "${WK_BOOTSTRAP_PACKAGE:-}" ]]; then
  test -r "$WK_BOOTSTRAP_PACKAGE"
  test "$(rpm -qp --queryformat '%{NAME}' "$WK_BOOTSTRAP_PACKAGE")" = wukongim-release
  test "$(rpm -qp --queryformat '%{ARCH}' "$WK_BOOTSTRAP_PACKAGE")" = noarch
  test -z "$(rpm -qp --scripts "$WK_BOOTSTRAP_PACKAGE")"
  test "$(rpm -qpl "$WK_BOOTSTRAP_PACKAGE" | sort)" = $'/etc/pki/rpm-gpg/RPM-GPG-KEY-wukongim-preview\n/etc/yum.repos.d/wukongim-preview.repo'

  rpm --dbpath /tmp/bootstrap-rpmdb --initdb
  rpmkeys --dbpath /tmp/bootstrap-rpmdb --import /keys/rpm-preview.asc
  rpmkeys --dbpath /tmp/bootstrap-rpmdb --checksig "$WK_BOOTSTRAP_PACKAGE" \
    | tee /tmp/bootstrap-rpm-checksig.txt
  grep -Eq 'digests signatures OK$' /tmp/bootstrap-rpm-checksig.txt
  rpm --dbpath /tmp/bootstrap-rpmdb --install "$WK_BOOTSTRAP_PACKAGE"
  rpm --dbpath /tmp/bootstrap-rpmdb -q wukongim-release >/dev/null

  installed_repo=/etc/yum.repos.d/wukongim-preview.repo
  installed_key=/etc/pki/rpm-gpg/RPM-GPG-KEY-wukongim-preview
  test -f "$installed_repo" && test ! -L "$installed_repo"
  test -f "$installed_key" && test ! -L "$installed_key"
  test "$(stat -c '%a:%U:%G' "$installed_repo")" = 644:root:root
  test "$(stat -c '%a:%U:%G' "$installed_key")" = 644:root:root
  cat >/tmp/expected-wukongim-preview.repo <<'EOF'
[wukongim-preview]
name=WuKongIM preview
baseurl=https://packages.githubim.com/rpm/preview/el/9/x86_64
enabled=1
includepkgs=wukongim,wukongim-release
gpgcheck=1
repo_gpgcheck=1
gpgkey=file:///etc/pki/rpm-gpg/RPM-GPG-KEY-wukongim-preview
sslverify=1
metadata_expire=0
skip_if_unavailable=0
EOF
  test "$(sha256sum < /tmp/expected-wukongim-preview.repo)" = \
    "$(sha256sum < "$installed_repo")"
  cp "$installed_repo" /tmp/repos.d/wukongim-preview.repo
  # DNF resolves file:// repository keys in the container namespace, outside
  # the installroot.  Point the validation copy at the key installed inside
  # that isolated root; the packaged repository file above remains unchanged.
  sed -i "s#^gpgkey=file:///etc/pki/rpm-gpg/RPM-GPG-KEY-wukongim-preview\$#gpgkey=file://$installed_key#" \
    /tmp/repos.d/wukongim-preview.repo
  if ! grep -Fxq "baseurl=$WK_REPOSITORY_URL" /tmp/repos.d/wukongim-preview.repo; then
    sed -i "s#^baseurl=https://packages.githubim.com/rpm/preview/el/9/x86_64\$#baseurl=$WK_REPOSITORY_URL#" \
      /tmp/repos.d/wukongim-preview.repo
  fi
else
  cat >/tmp/repos.d/wukongim-preview.repo <<EOF
[wukongim-preview]
name=WuKongIM preview
baseurl=$WK_REPOSITORY_URL
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=file:///keys/rpm-preview.asc
sslverify=1
metadata_expire=0
skip_if_unavailable=0
EOF
fi
dnf_options=(
  --quiet
  --assumeyes
  --installroot="$client_root"
  --releasever=9
  --disablerepo=*
  --enablerepo=wukongim-preview
  --setopt=reposdir=/tmp/repos.d
  --setopt=cachedir=/tmp/dnf-cache
  --setopt=persistdir=/tmp/dnf-state
  --setopt=keepcache=False
  --setopt=install_weak_deps=False
)
dnf "${dnf_options[@]}" makecache --refresh
dnf "${dnf_options[@]}" install --downloadonly \
  --downloaddir=/downloads wukongim
if rpm --root "$client_root" -q wukongim >/dev/null 2>&1; then
  echo 'DNF download-only validation installed wukongim' >&2
  exit 1
fi
if [[ -n "${WK_BOOTSTRAP_PACKAGE:-}" ]]; then
  rpm --dbpath /tmp/bootstrap-rpmdb -q wukongim-release >/dev/null
fi
shopt -s nullglob
packages=(/downloads/*.rpm)
((${#packages[@]} == 1))
rpm --dbpath /tmp/rpmdb --initdb
rpmkeys --dbpath /tmp/rpmdb --import /keys/rpm-preview.asc
rpmkeys --dbpath /tmp/rpmdb --checksig "${packages[0]}" \
  | tee /tmp/rpm-checksig.txt
grep -Eq 'digests signatures OK$' /tmp/rpm-checksig.txt
""".strip()


REPOSITORY_ENTRYPOINT_APT_SCRIPT = r"""
set -eu
export LC_ALL=C
if dpkg-query -W -f='${db:Status-Abbrev}\n' wukongim 2>/dev/null | grep -q '^ii '; then
  echo 'wukongim was already installed before repository setup' >&2
  exit 1
fi
sh /repo
dpkg-query -W -f='${db:Status-Abbrev}\n' wukongim-archive-keyring | grep -q '^ii '
test -f /etc/apt/sources.list.d/wukongim-preview.sources
test -f /usr/share/keyrings/wukongim-archive-keyring.pgp
sh /repo
dpkg-query -W -f='${db:Status-Abbrev}\n' wukongim-archive-keyring | grep -q '^ii '
if dpkg-query -W -f='${db:Status-Abbrev}\n' wukongim 2>/dev/null | grep -q '^ii '; then
  echo 'repository setup installed wukongim' >&2
  exit 1
fi
""".strip()


REPOSITORY_ENTRYPOINT_RPM_SCRIPT = r"""
set -eu
export LC_ALL=C
if rpm -q wukongim >/dev/null 2>&1; then
  echo 'wukongim was already installed before repository setup' >&2
  exit 1
fi
sh /repo
rpm -q wukongim-release >/dev/null
test -f /etc/yum.repos.d/wukongim-preview.repo
test -f /etc/pki/rpm-gpg/RPM-GPG-KEY-wukongim-preview
sh /repo
rpm -q wukongim-release >/dev/null
if rpm -q wukongim >/dev/null 2>&1; then
  echo 'repository setup installed wukongim' >&2
  exit 1
fi
""".strip()


class ClientValidationError(RuntimeError):
    """A clean package client failed a production publication invariant."""


def _require(condition: bool, message: str) -> None:
    if not condition:
        raise ClientValidationError(message)


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def _canonical_json(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":")).encode("utf-8") + b"\n"


def _exact_object(value: Any, fields: set[str], label: str) -> dict[str, Any]:
    _require(isinstance(value, dict), f"{label} must be an object")
    _require(set(value) == fields, f"{label} fields must be exactly {sorted(fields)}")
    return value


def _reject_duplicate_pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        _require(key not in result, f"duplicate JSON key: {key}")
        result[key] = value
    return result


def _read_regular(path: Path, label: str, maximum: int) -> bytes:
    flags = os.O_RDONLY
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    try:
        descriptor = os.open(path, flags)
    except OSError as error:
        raise ClientValidationError(f"cannot open {label}: {error}") from error
    try:
        before = os.fstat(descriptor)
        _require(stat.S_ISREG(before.st_mode), f"{label} must be a real regular file")
        _require(before.st_nlink == 1, f"{label} must not be hard-linked")
        _require(0 < before.st_size <= maximum, f"{label} has an invalid size")
        with os.fdopen(descriptor, "rb", closefd=False) as source:
            data = source.read(maximum + 1)
        after = os.fstat(descriptor)
        _require(
            len(data) == before.st_size
            and after.st_dev == before.st_dev
            and after.st_ino == before.st_ino
            and after.st_size == before.st_size
            and after.st_mtime_ns == before.st_mtime_ns,
            f"{label} changed while it was read",
        )
        return data
    except OSError as error:
        raise ClientValidationError(f"cannot read {label}: {error}") from error
    finally:
        os.close(descriptor)


def _real_directory(path: Path, label: str) -> Path:
    try:
        metadata = path.lstat()
    except OSError as error:
        raise ClientValidationError(f"cannot inspect {label}: {error}") from error
    _require(stat.S_ISDIR(metadata.st_mode), f"{label} must be a real directory")
    try:
        return path.resolve(strict=True)
    except OSError as error:
        raise ClientValidationError(f"cannot resolve {label}: {error}") from error


def _load_json(path: Path, label: str) -> tuple[dict[str, Any], bytes]:
    data = _read_regular(path, label, MAX_CONTROL_BYTES)
    try:
        value = json.loads(data)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ClientValidationError(f"{label} is not valid JSON") from error
    _require(isinstance(value, dict), f"{label} must be a JSON object")
    return value, data


def _load_canonical_snapshot(path: Path) -> tuple[dict[str, Any], bytes]:
    data = _read_regular(path, "reviewed snapshot", MAX_CONTROL_BYTES)
    try:
        value = json.loads(data, object_pairs_hook=_reject_duplicate_pairs)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ClientValidationError("reviewed snapshot is not valid JSON") from error
    _require(isinstance(value, dict), "reviewed snapshot must be a JSON object")
    _require(data == _canonical_json(value), "reviewed snapshot must use canonical JSON encoding")
    return value, data


def _safe_site_path(value: Any, family: str, label: str) -> str:
    _require(isinstance(value, str) and value, f"{label} must be a non-empty path")
    path = PurePosixPath(value)
    _require(
        not path.is_absolute()
        and "\\" not in value
        and "\x00" not in value
        and path.as_posix() == value
        and all(part not in {"", ".", ".."} for part in path.parts),
        f"{label} is unsafe",
    )
    _require(all(SAFE_COMPONENT_RE.fullmatch(part) is not None for part in path.parts),
             f"{label} is unsafe")
    suffix = ".deb" if family == "apt" else ".rpm"
    prefix = "apt/pool/" if family == "apt" else "rpm/preview/el/9/x86_64/Packages/"
    _require(value.startswith(prefix) and value.endswith(suffix), f"{label} is not canonical")
    return value


def _expected_bootstrap_package(family: str, version: str) -> dict[str, str]:
    if family == "apt":
        filename = f"wukongim-archive-keyring_{version}_all.deb"
        return {
            "name": "wukongim-archive-keyring",
            "architecture": "all",
            "filename": filename,
            "repository_path": f"apt/pool/main/w/wukongim/{filename}",
            "download_path": f"bootstrap/{filename}",
        }
    _require(family == "rpm", "bootstrap package family is unsupported")
    filename = f"wukongim-release-{version}-1.noarch.rpm"
    return {
        "name": "wukongim-release",
        "architecture": "noarch",
        "filename": filename,
        "repository_path": f"rpm/preview/el/9/x86_64/Packages/{filename}",
        "download_path": f"bootstrap/{filename}",
    }


def _safe_bootstrap_path(value: Any, expected: str, label: str) -> str:
    _require(isinstance(value, str) and value == expected, f"{label} must be {expected}")
    path = PurePosixPath(value)
    _require(
        not path.is_absolute()
        and "\\" not in value
        and "\x00" not in value
        and path.as_posix() == value
        and all(part not in {"", ".", ".."} for part in path.parts)
        and all(SAFE_COMPONENT_RE.fullmatch(part) is not None for part in path.parts),
        f"{label} is unsafe",
    )
    return value


def _validate_bootstrap_manifest_bytes(
    data: bytes, label: str
) -> tuple[dict[str, Any], str]:
    _require(0 < len(data) <= MAX_CONTROL_BYTES, f"{label} has an invalid size")
    try:
        value = json.loads(data, object_pairs_hook=_reject_duplicate_pairs)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ClientValidationError(f"{label} is not valid JSON") from error
    manifest = _exact_object(value, BOOTSTRAP_FIELDS, label)
    _require(data == _canonical_json(manifest), f"{label} must use canonical JSON encoding")
    _require(manifest["schema"] == BOOTSTRAP_SCHEMA,
             f"{label} schema must be {BOOTSTRAP_SCHEMA}")
    version = manifest["version"]
    _require(isinstance(version, str)
             and RELEASE_VERSION_RE.fullmatch(version) is not None,
             f"{label} version must be strict release SemVer")
    packages = _exact_object(manifest["packages"], {"apt", "rpm"},
                             f"{label} packages")
    for family in ("apt", "rpm"):
        item = _exact_object(packages[family], BOOTSTRAP_PACKAGE_FIELDS,
                             f"{label} {family} package")
        expected = _expected_bootstrap_package(family, version)
        _require(item["version"] == version,
                 f"{label} {family} package version differs from the manifest")
        for field in ("name", "architecture", "filename"):
            _require(item[field] == expected[field],
                     f"{label} {family} package {field} must be {expected[field]}")
        _safe_bootstrap_path(
            item["repository_path"], expected["repository_path"],
            f"{label} {family} repository_path",
        )
        _safe_bootstrap_path(
            item["download_path"], expected["download_path"],
            f"{label} {family} download_path",
        )
        for field in ("source_sha256", "published_sha256"):
            _require(isinstance(item[field], str)
                     and SHA256_RE.fullmatch(item[field]) is not None,
                     f"{label} {family} {field} is invalid")
        for field in ("source_size", "published_size"):
            _require(type(item[field]) is int
                     and 0 < item[field] <= MAX_BOOTSTRAP_BYTES,
                     f"{label} {family} {field} is invalid")
        _require(type(item["new"]) is bool,
                 f"{label} {family} new must be a boolean")
        if family == "apt":
            _require(
                (item["source_sha256"], item["source_size"])
                == (item["published_sha256"], item["published_size"]),
                f"{label} APT source and published bytes must match",
            )
    return manifest, _sha256(data)


def _bootstrapless_legacy(identity: tuple[Any, Any]) -> bool:
    return identity == LEGACY_BOOTSTRAPLESS_IDENTITY


def _validate_bootstrap_payload(
    data: bytes, item: dict[str, Any], label: str
) -> None:
    _require(isinstance(data, bytes)
             and len(data) == item["published_size"],
             f"{label} size differs from the bootstrap manifest")
    _require(_sha256(data) == item["published_sha256"],
             f"{label} digest differs from the bootstrap manifest")


def _repository_entrypoint_facts(
    data: bytes,
    manifest: dict[str, Any],
    rpm_certificate: bytes,
    label: str,
) -> dict[str, Any]:
    _require(isinstance(data, bytes)
             and 0 < len(data) <= MAX_REPOSITORY_ENTRYPOINT_BYTES,
             f"{label} has an invalid size")
    try:
        expected = COMPOSER.repository_entrypoint_bytes(
            manifest, _sha256(rpm_certificate)
        )
    except COMPOSER.CompositionError as error:
        raise ClientValidationError(str(error)) from error
    _require(data == expected,
             f"{label} differs from reviewed bootstrap identities")
    return {
        "path": COMPOSER.REPOSITORY_ENTRYPOINT_PATH,
        "sha256": _sha256(data),
        "size": len(data),
    }


def _repository_entrypoint_curl_shim(manifest: dict[str, Any]) -> bytes:
    """Return a network-free curl shim exposing only already-verified inputs."""
    apt = manifest["packages"]["apt"]
    rpm = manifest["packages"]["rpm"]
    return f"""#!/bin/sh
set -eu
output=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output)
      [ "$#" -ge 2 ] || exit 64
      output=$2
      shift 2
      continue
      ;;
    https://*) url=$1 ;;
  esac
  shift
done
[ -n "$output" ] || exit 64
case "$url" in
  {COMPOSER.REPOSITORY_ORIGIN}/{apt['download_path']})
    source_path=/bootstrap/{apt['filename']}
    ;;
  {COMPOSER.REPOSITORY_ORIGIN}/{rpm['download_path']})
    source_path=/bootstrap/{rpm['filename']}
    ;;
  {COMPOSER.REPOSITORY_ORIGIN}/keys/rpm-preview.asc)
    source_path=/keys/rpm-preview.asc
    ;;
  *)
    printf '%s\n' "unexpected repository setup URL: $url" >&2
    exit 65
    ;;
esac
cp "$source_path" "$output"
""".encode("ascii")


def _validate_local_bootstrap(
    site: Path, identity: tuple[Any, Any], rpm_certificate: bytes
) -> dict[str, Any] | None:
    manifest_path = site / "bootstrap/manifest.json"
    try:
        manifest_data = _read_regular(
            manifest_path, "published bootstrap manifest", MAX_CONTROL_BYTES
        )
    except ClientValidationError as error:
        if not os.path.lexists(manifest_path) and _bootstrapless_legacy(identity):
            return None
        raise error
    manifest, manifest_sha256 = _validate_bootstrap_manifest_bytes(
        manifest_data, "published bootstrap manifest"
    )
    payloads: dict[str, bytes] = {}
    for family in ("apt", "rpm"):
        item = manifest["packages"][family]
        repository = _read_regular(
            site.joinpath(*PurePosixPath(item["repository_path"]).parts),
            f"published {family} bootstrap repository package",
            MAX_BOOTSTRAP_BYTES,
        )
        direct = _read_regular(
            site.joinpath(*PurePosixPath(item["download_path"]).parts),
            f"published {family} bootstrap direct package",
            MAX_BOOTSTRAP_BYTES,
        )
        _validate_bootstrap_payload(
            repository, item, f"published {family} bootstrap repository package"
        )
        _validate_bootstrap_payload(
            direct, item, f"published {family} bootstrap direct package"
        )
        _require(direct == repository,
                 f"published {family} bootstrap direct and repository packages differ")
        payloads[family] = direct
    entrypoint_path = site / COMPOSER.REPOSITORY_ENTRYPOINT_PATH
    if not os.path.lexists(entrypoint_path):
        _require(
            identity in LEGACY_REPOSITORY_ENTRYPOINT_FREE_IDENTITIES,
            "published repository setup entrypoint is required",
        )
        entrypoint = None
        entrypoint_data = None
    else:
        entrypoint_data = _read_regular(
            entrypoint_path,
            "published repository setup entrypoint",
            MAX_REPOSITORY_ENTRYPOINT_BYTES,
        )
        entrypoint = _repository_entrypoint_facts(
            entrypoint_data,
            manifest,
            rpm_certificate,
            "published repository setup entrypoint",
        )
    return {
        "manifest": manifest,
        "manifest_sha256": manifest_sha256,
        "payloads": payloads,
        "repository_entrypoint": entrypoint,
        "repository_entrypoint_bytes": entrypoint_data,
    }


def _validate_remote_bootstrap(
    base_url: str,
    identity: tuple[Any, Any],
    rpm_certificate: bytes,
    fetcher: Callable[[str, int], bytes],
) -> dict[str, Any] | None:
    manifest_url = f"{base_url}/bootstrap/manifest.json"
    try:
        manifest_data = fetcher(manifest_url, MAX_CONTROL_BYTES)
    except (ClientValidationError, KeyError, OSError, urllib.error.HTTPError) as error:
        if _bootstrapless_legacy(identity):
            return None
        raise ClientValidationError(
            f"remote bootstrap manifest is required for audit identity {identity[0]}"
        ) from error
    _require(isinstance(manifest_data, bytes),
             "remote bootstrap manifest response is invalid")
    manifest, manifest_sha256 = _validate_bootstrap_manifest_bytes(
        manifest_data, "remote bootstrap manifest"
    )
    payloads: dict[str, bytes] = {}
    for family in ("apt", "rpm"):
        item = manifest["packages"][family]
        repository = fetcher(
            f"{base_url}/{item['repository_path']}", MAX_BOOTSTRAP_BYTES
        )
        direct = fetcher(f"{base_url}/{item['download_path']}", MAX_BOOTSTRAP_BYTES)
        _validate_bootstrap_payload(
            repository, item, f"remote {family} bootstrap repository package"
        )
        _validate_bootstrap_payload(
            direct, item, f"remote {family} bootstrap direct package"
        )
        _require(direct == repository,
                 f"remote {family} bootstrap direct and repository packages differ")
        payloads[family] = direct
    try:
        entrypoint_data = fetcher(
            f"{base_url}/{COMPOSER.REPOSITORY_ENTRYPOINT_PATH}",
            MAX_REPOSITORY_ENTRYPOINT_BYTES,
        )
    except (ClientValidationError, KeyError, OSError, urllib.error.HTTPError) as error:
        if identity not in LEGACY_REPOSITORY_ENTRYPOINT_FREE_IDENTITIES:
            raise ClientValidationError(
                f"remote repository setup entrypoint is required for audit identity {identity[0]}"
            ) from error
        entrypoint = None
    else:
        entrypoint = _repository_entrypoint_facts(
            entrypoint_data,
            manifest,
            rpm_certificate,
            "remote repository setup entrypoint",
        )
    return {
        "manifest": manifest,
        "manifest_sha256": manifest_sha256,
        "payloads": payloads,
        "repository_entrypoint": entrypoint,
        "repository_entrypoint_bytes": (
            None if entrypoint is None else entrypoint_data
        ),
    }


def _validate_snapshot_public_keys(
    value: Any,
    certificates: dict[str, tuple[Path, bytes]],
) -> None:
    public_keys = _exact_object(value, {"apt", "rpm"}, "snapshot public_keys")
    all_fingerprints: list[str] = []
    for family in ("apt", "rpm"):
        certificate_path, certificate = certificates[family]
        key = _exact_object(
            public_keys[family], PUBLIC_KEY_FIELDS, f"snapshot {family} public key"
        )
        reviewed_path = f"keys/{family}-preview.asc"
        _require(key["path"] == reviewed_path,
                 f"snapshot {family} public-key path is invalid")
        _require(key["sha256"] == _sha256(certificate),
                 f"snapshot {family} public-key digest differs from the reviewed certificate")
        _require(type(key["size"]) is int and key["size"] == len(certificate),
                 f"snapshot {family} public-key size differs from the reviewed certificate")
        fingerprints: list[str] = []
        for field in (
            "primary_fingerprint",
            "current_signing_subkey_fingerprint",
            "next_signing_subkey_fingerprint",
        ):
            fingerprint = key[field]
            _require(isinstance(fingerprint, str)
                     and FINGERPRINT_RE.fullmatch(fingerprint) is not None,
                     f"snapshot {family} {field} is invalid")
            fingerprints.append(fingerprint)
        historical = key["historical_signing_subkey_fingerprints"]
        _require(isinstance(historical, list)
                 and all(isinstance(item, str)
                         and FINGERPRINT_RE.fullmatch(item) is not None
                         for item in historical),
                 f"snapshot {family} historical signing-subkey fingerprints are invalid")
        _require(historical == sorted(set(historical)),
                 f"snapshot {family} historical signing-subkey fingerprints must be unique and sorted")
        fingerprints.extend(historical)
        _require(len(fingerprints) == len(set(fingerprints)),
                 f"snapshot {family} public-key fingerprints must be distinct")
        all_fingerprints.extend(fingerprints)
        _require(certificate_path.name != "", f"reviewed {family} certificate path is invalid")
    _require(len(all_fingerprints) == len(set(all_fingerprints)),
             "snapshot APT and RPM public-key fingerprints must be distinct")
    _require(len({item[-16:] for item in all_fingerprints}) == len(all_fingerprints),
             "snapshot public-key 16-hex key IDs must be distinct")
    _require(len({item[-8:] for item in all_fingerprints}) == len(all_fingerprints),
             "snapshot public-key 8-hex key IDs must be distinct")


def _validate_snapshot_auxiliary(value: dict[str, Any]) -> None:
    retirement = _exact_object(
        value["retirement"], {"phase", "version", "not_before"}, "snapshot retirement"
    )
    _require(retirement["phase"] in {"none", "indexes_removed"},
             "snapshot retirement phase is invalid")
    if retirement["phase"] == "none":
        _require(retirement["version"] is None and retirement["not_before"] is None,
                 "snapshot retirement none state is invalid")
    else:
        _require(isinstance(retirement["version"], str)
                 and VERSION_RE.fullmatch(retirement["version"]) is not None,
                 "snapshot retirement version is invalid")
        _require(isinstance(retirement["not_before"], str)
                 and UTC_RE.fullmatch(retirement["not_before"]) is not None,
                 "snapshot retirement not_before is invalid")

    toolchain = _exact_object(value["toolchain"], TOOLCHAIN_FIELDS, "snapshot toolchain")
    _require(toolchain["image"] == "ghcr.io/wukongim/native-package-signing-toolchain",
             "snapshot toolchain image is invalid")
    _require(isinstance(toolchain["digest"], str)
             and OCI_DIGEST_RE.fullmatch(toolchain["digest"]) is not None,
             "snapshot toolchain digest is invalid")
    _require(isinstance(toolchain["workflow_sha"], str)
             and SHA1_RE.fullmatch(toolchain["workflow_sha"]) is not None,
             "snapshot toolchain workflow_sha is invalid")
    _require(isinstance(toolchain["manifest_sha256"], str)
             and SHA256_RE.fullmatch(toolchain["manifest_sha256"]) is not None,
             "snapshot toolchain manifest_sha256 is invalid")
    _require(type(toolchain["manifest_size"]) is int and toolchain["manifest_size"] > 0,
             "snapshot toolchain manifest_size is invalid")

    source = value["source_attestations"]
    if source is None:
        return
    source = _exact_object(source, SOURCE_ATTESTATION_FIELDS,
                           "snapshot source_attestations")
    _require(isinstance(source["summary_sha256"], str)
             and SHA256_RE.fullmatch(source["summary_sha256"]) is not None,
             "snapshot source-attestation summary digest is invalid")
    files = source["files"]
    _require(isinstance(files, list) and len(files) == 8,
             "snapshot source-attestation files must contain eight entries")
    prior = ""
    summary_digest: str | None = None
    for index, raw in enumerate(files):
        item = _exact_object(raw, ARTIFACT_FIELDS,
                             f"snapshot source-attestation file {index}")
        path = item["path"]
        _require(isinstance(path, str) and path.startswith("audit/source-attestations/")
                 and PurePosixPath(path).as_posix() == path
                 and len(PurePosixPath(path).parts) == 3
                 and all(SAFE_COMPONENT_RE.fullmatch(part) is not None
                         for part in PurePosixPath(path).parts),
                 f"snapshot source-attestation file {index} path is unsafe")
        _require(path > prior, "snapshot source-attestation paths must be unique and sorted")
        prior = path
        _require(isinstance(item["sha256"], str)
                 and SHA256_RE.fullmatch(item["sha256"]) is not None,
                 f"snapshot source-attestation file {index} digest is invalid")
        _require(type(item["size"]) is int and item["size"] > 0,
                 f"snapshot source-attestation file {index} size is invalid")
        if PurePosixPath(path).name == "source-attestations.json":
            summary_digest = item["sha256"]
    _require(summary_digest == source["summary_sha256"],
             "snapshot source-attestation summary digest differs from its inventory")


def _validate_reviewed_remote_snapshot(
    snapshot_path: Path,
    status: dict[str, Any],
    certificates: dict[str, tuple[Path, bytes]],
    base_url: str,
    fetcher: Callable[[str, int], bytes],
) -> tuple[dict[str, dict[str, list[str]]], str]:
    snapshot, snapshot_bytes = _load_canonical_snapshot(snapshot_path)
    snapshot = _exact_object(snapshot, SNAPSHOT_FIELDS, "reviewed snapshot")
    _require(snapshot["schema"] == SNAPSHOT_SCHEMA,
             f"reviewed snapshot schema must be {SNAPSHOT_SCHEMA}")
    snapshot_sha256 = _sha256(snapshot_bytes)
    _require(snapshot_sha256 == status["snapshot_sha256"],
             "reviewed snapshot SHA-256 differs from remote status")
    _require(type(snapshot["audit_release_id"]) is int
             and snapshot["audit_release_id"] > 0,
             "reviewed snapshot audit_release_id is invalid")
    _require(snapshot["audit_release_id"] == status["audit_release_id"],
             "reviewed snapshot audit_release_id differs from remote status")
    _require(isinstance(snapshot["control_sha"], str)
             and SHA1_RE.fullmatch(snapshot["control_sha"]) is not None,
             "reviewed snapshot control_sha is invalid")
    _require(snapshot["control_sha"] == status["control_sha"],
             "reviewed snapshot control_sha differs from remote status")
    _validate_snapshot_public_keys(snapshot["public_keys"], certificates)
    _validate_snapshot_auxiliary(snapshot)

    releases_value = snapshot["releases"]
    _require(isinstance(releases_value, list), "snapshot releases must be an array")
    releases: dict[str, dict[str, Any]] = {}
    for index, raw in enumerate(releases_value):
        release = _exact_object(raw, RELEASE_FIELDS, f"snapshot release {index}")
        version = release["version"]
        _require(isinstance(version, str) and VERSION_RE.fullmatch(version) is not None,
                 f"snapshot release {index} version is invalid")
        # The composer preserves reviewed manifest order for release facts.
        # Only payload inventories use lexical order; beta.12 may follow beta.9.
        _require(version not in releases, "snapshot release versions must be unique")
        _require(isinstance(release["source_sha"], str)
                 and SHA1_RE.fullmatch(release["source_sha"]) is not None,
                 f"snapshot release {version} source_sha is invalid")
        for field in ("source_release_id", "package_release_id"):
            _require(type(release[field]) is int and release[field] > 0,
                     f"snapshot release {version} {field} is invalid")
        for field in ("deb_sha256", "rpm_sha256"):
            _require(isinstance(release[field], str)
                     and SHA256_RE.fullmatch(release[field]) is not None,
                     f"snapshot release {version} {field} is invalid")
        _require(release["state"] in {"active", "index_removed"},
                 f"snapshot release {version} state is invalid")
        if release["state"] == "active":
            _require(release["not_before"] is None,
                     f"snapshot active release {version} not_before must be null")
        else:
            _require(isinstance(release["not_before"], str)
                     and UTC_RE.fullmatch(release["not_before"]) is not None,
                     f"snapshot retained release {version} not_before is invalid")
        releases[version] = release
    _require(bool(releases), "snapshot releases must not be empty")

    retirement = snapshot["retirement"]
    retained = [version for version, item in releases.items()
                if item["state"] == "index_removed"]
    if retirement["phase"] == "none":
        _require(not retained, "snapshot retirement state differs from releases")
    else:
        _require(retained == [retirement["version"]]
                 and releases[retirement["version"]]["not_before"]
                 == retirement["not_before"],
                 "snapshot retirement state differs from releases")

    payloads = _exact_object(snapshot["payloads"], {"apt", "rpm"},
                             "snapshot payloads")
    expected: dict[str, dict[str, list[str]]] = {}
    for family, source_field in (("apt", "deb_sha256"), ("rpm", "rpm_sha256")):
        values = payloads[family]
        _require(isinstance(values, list), f"snapshot {family} payloads must be an array")
        versions: list[str] = []
        seen_paths: set[str] = set()
        indexed_digests: dict[str, list[str]] = {}
        for index, raw in enumerate(values):
            item = _exact_object(raw, PAYLOAD_FIELDS,
                                 f"snapshot {family} payload {index}")
            version = item["version"]
            _require(isinstance(version, str) and version in releases,
                     f"snapshot {family} payload {index} version is invalid")
            versions.append(version)
            path = _safe_site_path(item["path"], family,
                                   f"snapshot {family} payload {index} path")
            _require(path not in seen_paths,
                     f"snapshot {family} payload paths must be unique")
            seen_paths.add(path)
            _require(isinstance(item["source_sha256"], str)
                     and SHA256_RE.fullmatch(item["source_sha256"]) is not None,
                     f"snapshot {family} payload {index} source digest is invalid")
            _require(item["source_sha256"] == releases[version][source_field],
                     f"snapshot {family} payload {version} source digest differs from release")
            published = item["published_sha256"]
            _require(isinstance(published, str)
                     and SHA256_RE.fullmatch(published) is not None,
                     f"snapshot {family} payload {index} published digest is invalid")
            if family == "apt":
                _require(published == item["source_sha256"],
                         "snapshot APT published digest differs from its source")
            indexed = item["indexed"]
            _require(type(indexed) is bool
                     and indexed == (releases[version]["state"] == "active"),
                     f"snapshot {family} payload {version} indexed state differs from release")
            remote = fetcher(f"{base_url}/{path}", MAX_DOWNLOAD_BYTES)
            _require(isinstance(remote, bytes)
                     and 0 < len(remote) <= MAX_DOWNLOAD_BYTES,
                     f"remote {family} payload {path} response is invalid")
            _require(_sha256(remote) == published,
                     f"remote {family} payload {path} digest differs from reviewed snapshot")
            if indexed:
                indexed_digests.setdefault(published, []).append(version)
        _require(versions == sorted(releases),
                 f"snapshot {family} payload versions must exactly cover releases in order")
        _require(bool(indexed_digests), f"snapshot contains no indexed {family} payload")
        expected[family] = indexed_digests
    return expected, snapshot_sha256


def _validate_local_inputs(
    site_root: Path,
    snapshot_path: Path,
    certificates: dict[str, tuple[Path, bytes]],
) -> tuple[Path, dict[str, dict[str, list[str]]], str, tuple[int, str]]:
    site = _real_directory(site_root, "site root")
    snapshot, snapshot_bytes = _load_json(snapshot_path, "snapshot")
    _require(snapshot.get("schema") == SNAPSHOT_SCHEMA,
             f"snapshot schema must be {SNAPSHOT_SCHEMA}")
    audit_release_id = snapshot.get("audit_release_id")
    control_sha = snapshot.get("control_sha")
    _require(type(audit_release_id) is int and audit_release_id > 0,
             "snapshot audit_release_id is invalid")
    _require(isinstance(control_sha, str)
             and SHA1_RE.fullmatch(control_sha) is not None,
             "snapshot control_sha is invalid")
    payloads = snapshot.get("payloads")
    public_keys = snapshot.get("public_keys")
    _require(isinstance(payloads, dict), "snapshot payloads must be an object")
    _require(isinstance(public_keys, dict), "snapshot public_keys must be an object")

    expected: dict[str, dict[str, list[str]]] = {}
    for family in ("apt", "rpm"):
        certificate_path, certificate = certificates[family]
        key = public_keys.get(family)
        _require(isinstance(key, dict), f"snapshot {family} public key must be an object")
        reviewed_path = f"keys/{family}-preview.asc"
        _require(key.get("path") == reviewed_path,
                 f"snapshot {family} public-key path is invalid")
        _require(key.get("sha256") == _sha256(certificate),
                 f"snapshot {family} public-key digest differs from the reviewed certificate")
        _require(key.get("size") == len(certificate),
                 f"snapshot {family} public-key size differs from the reviewed certificate")
        published_certificate = _read_regular(
            site.joinpath(*PurePosixPath(reviewed_path).parts),
            f"published {family} public certificate",
            MAX_CERTIFICATE_BYTES,
        )
        _require(
            published_certificate == certificate,
            f"published {family} public certificate differs from {certificate_path}",
        )

        entries = payloads.get(family)
        _require(isinstance(entries, list), f"snapshot {family} payloads must be an array")
        digest_versions: dict[str, list[str]] = {}
        seen_paths: set[str] = set()
        for index, raw in enumerate(entries):
            _require(isinstance(raw, dict),
                     f"snapshot {family} payload {index} must be an object")
            path = _safe_site_path(raw.get("path"), family,
                                   f"snapshot {family} payload {index} path")
            _require(path not in seen_paths, f"snapshot {family} payload paths must be unique")
            seen_paths.add(path)
            indexed = raw.get("indexed")
            _require(type(indexed) is bool,
                     f"snapshot {family} payload {index} indexed must be a boolean")
            digest = raw.get("published_sha256")
            version = raw.get("version")
            _require(isinstance(digest, str) and SHA256_RE.fullmatch(digest) is not None,
                     f"snapshot {family} payload {index} digest is invalid")
            _require(isinstance(version, str) and version,
                     f"snapshot {family} payload {index} version is invalid")
            if indexed:
                digest_versions.setdefault(digest, []).append(version)
        _require(digest_versions, f"snapshot contains no indexed {family} payload")
        expected[family] = digest_versions
    return site, expected, _sha256(snapshot_bytes), (audit_release_id, control_sha)


def _validated_base_url(value: str) -> str:
    parsed = urllib.parse.urlsplit(value)
    _require(
        parsed.scheme == "https"
        and bool(parsed.hostname)
        and parsed.username is None
        and parsed.password is None,
        "base URL must be an absolute HTTPS URL without credentials",
    )
    _require(not parsed.query and not parsed.fragment,
             "base URL must not contain a query or fragment")
    _require(not any(character.isspace() or ord(character) < 0x20 for character in value),
             "base URL contains an unsafe character")
    return value.rstrip("/")


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(
        self, req: Any, fp: Any, code: int, msg: str, headers: Any, newurl: str
    ) -> None:
        return None


def _fetch_public_file(url: str, maximum: int) -> bytes:
    request = urllib.request.Request(
        url,
        method="GET",
        headers={"User-Agent": "wukongim-package-client-validator/1"},
    )
    opener = urllib.request.build_opener(_NoRedirect())
    try:
        with opener.open(request, timeout=30) as response:
            data = response.read(maximum + 1)
    except (OSError, urllib.error.HTTPError) as error:
        raise ClientValidationError(f"public endpoint read failed for {url}: {error}") from error
    _require(len(data) <= maximum, f"public endpoint response is too large for {url}")
    return data


def _validate_remote_inputs(
    base_url: str,
    certificates: dict[str, tuple[Path, bytes]],
    fetcher: Callable[[str, int], bytes],
) -> tuple[dict[str, Any], bytes]:
    status_bytes = fetcher(f"{base_url}/status.json", MAX_CONTROL_BYTES)
    _require(isinstance(status_bytes, bytes)
             and 0 < len(status_bytes) <= MAX_CONTROL_BYTES,
             "remote status.json response is invalid")
    try:
        status = json.loads(status_bytes)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ClientValidationError("remote status.json is not valid JSON") from error
    _require(isinstance(status, dict), "remote status.json must be an object")
    _require(status.get("schema") == STATUS_SCHEMA,
             f"remote status schema must be {STATUS_SCHEMA}")
    _require(status.get("apt") is True and status.get("rpm") is True
             and status.get("reason") == "ready",
             "remote repository status is not ready")
    _require(type(status.get("audit_release_id")) is int
             and status["audit_release_id"] > 0,
             "remote repository audit_release_id is invalid")
    _require(isinstance(status.get("control_sha"), str)
             and SHA1_RE.fullmatch(status["control_sha"]) is not None,
             "remote repository control_sha is invalid")
    _require(isinstance(status.get("snapshot_sha256"), str)
             and SHA256_RE.fullmatch(status["snapshot_sha256"]) is not None,
             "remote repository snapshot_sha256 is invalid")
    for family in ("apt", "rpm"):
        certificate_path, certificate = certificates[family]
        remote = fetcher(f"{base_url}/keys/{family}-preview.asc", MAX_CERTIFICATE_BYTES)
        _require(remote == certificate,
                 f"remote {family} public certificate differs from {certificate_path}")
    return status, status_bytes


def _host_ca_bundle() -> Path:
    for candidate in (
        Path("/etc/ssl/certs/ca-certificates.crt"),
        Path("/etc/pki/tls/certs/ca-bundle.crt"),
        Path("/etc/ssl/cert.pem"),
    ):
        try:
            if stat.S_ISREG(candidate.stat().st_mode):
                return candidate.resolve(strict=True)
        except OSError:
            continue
    raise ClientValidationError("remote validation requires a readable host CA bundle")


def _base_docker_command(
    downloads: Path,
    certificate: Path,
    family: str,
    site: Path | None,
    remote: bool,
    bootstrap_package: Path | None = None,
) -> list[str]:
    command = [
        "docker", "run", "--rm", "--platform", "linux/amd64",
        "--read-only", "--cap-drop", "ALL",
        "--security-opt", "no-new-privileges", "--pids-limit", "256",
        "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=256m",
        "--tmpfs", "/var/log:rw,noexec,nosuid,nodev,size=32m",
        "--volume", f"{downloads.resolve(strict=True)}:/downloads:rw",
        "--volume", f"{certificate.resolve(strict=True)}:/keys/{family}-preview.asc:ro",
    ]
    if family == "apt":
        command.extend((
            "--tmpfs", "/var/lib/apt:rw,noexec,nosuid,nodev,size=256m",
            "--tmpfs", "/var/cache/apt:rw,noexec,nosuid,nodev,size=256m",
        ))
        if bootstrap_package is not None:
            command.extend((
                "--tmpfs", "/etc/apt/sources.list.d:rw,noexec,nosuid,nodev,size=8m",
                "--tmpfs", "/usr/share/keyrings:rw,noexec,nosuid,nodev,size=8m",
            ))
    else:
        command.extend((
            "--tmpfs", "/var/cache/dnf:rw,noexec,nosuid,nodev,size=256m",
        ))
        if bootstrap_package is not None:
            command.extend((
                "--tmpfs", "/etc/pki/rpm-gpg:rw,noexec,nosuid,nodev,size=8m",
                "--tmpfs", "/etc/yum.repos.d:rw,noexec,nosuid,nodev,size=8m",
            ))
    if site is not None:
        command.extend(("--network", "none", "--volume", f"{site}:/site:ro"))
    elif remote and family == "apt":
        ca_bundle = _host_ca_bundle()
        command.extend((
            "--volume", f"{ca_bundle}:{APT_CA_BUNDLE_PATH}:ro",
        ))
    if bootstrap_package is not None:
        resolved_bootstrap = bootstrap_package.resolve(strict=True)
        command.extend((
            "--volume", f"{resolved_bootstrap}:/bootstrap/{bootstrap_package.name}:ro",
            "--env", f"WK_BOOTSTRAP_PACKAGE=/bootstrap/{bootstrap_package.name}",
        ))
    return command


def _client_command(
    image: str,
    downloads: Path,
    certificate: Path,
    family: str,
    site: Path | None,
    base_url: str | None,
    bootstrap_package: Path | None = None,
) -> list[str]:
    remote = base_url is not None
    command = _base_docker_command(
        downloads, certificate, family, site, remote, bootstrap_package
    )
    if family == "apt":
        repository = f"{base_url}/apt" if remote else "file:/site/apt"
        script = APT_SCRIPT
    else:
        repository = (
            f"{base_url}/rpm/preview/el/9/x86_64"
            if remote else "file:///site/rpm/preview/el/9/x86_64"
        )
        script = RPM_SCRIPT
    command.extend(("--env", f"WK_REPOSITORY_URL={repository}"))
    if remote and family == "apt":
        command.extend(("--env", f"WK_CA_BUNDLE={APT_CA_BUNDLE_PATH}"))
    command.extend((image, "bash", "-c", script))
    return command


def _repository_entrypoint_command(
    image: str,
    downloads: Path,
    family: str,
    entrypoint: Path,
    curl_shim: Path,
    bootstrap_package: Path,
    rpm_certificate: Path,
) -> list[str]:
    """Build one isolated, network-free execution of the public /repo path."""
    _require(family in {"apt", "rpm"}, "repository entrypoint family is invalid")
    command = [
        "docker", "run", "--rm", "--platform", "linux/amd64",
        "--network", "none", "--cap-drop", "ALL",
        "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE",
        "--cap-add", "FOWNER", "--cap-add", "SETGID", "--cap-add", "SETUID",
        "--security-opt", "no-new-privileges", "--pids-limit", "256",
        "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=256m",
        "--volume", f"{downloads.resolve(strict=True)}:/downloads:rw",
        "--volume", f"{entrypoint.resolve(strict=True)}:/repo:ro",
        "--volume", f"{curl_shim.resolve(strict=True)}:/usr/bin/curl:ro",
        "--volume", (
            f"{bootstrap_package.resolve(strict=True)}:"
            f"/bootstrap/{bootstrap_package.name}:ro"
        ),
    ]
    if family == "apt":
        command.extend(("--env", "DEBIAN_FRONTEND=noninteractive"))
        script = REPOSITORY_ENTRYPOINT_APT_SCRIPT
    else:
        command.extend((
            "--volume",
            f"{rpm_certificate.resolve(strict=True)}:/keys/rpm-preview.asc:ro",
        ))
        script = REPOSITORY_ENTRYPOINT_RPM_SCRIPT
    command.extend((image, "sh", "-c", script))
    return command


def _run(command: Sequence[str]) -> None:
    # Container package-manager output is diagnostic. Keep stdout reserved for
    # the canonical JSON receipt consumed by publication workflow checks.
    subprocess.run(
        list(command), check=True, stdout=sys.stderr, stderr=sys.stderr
    )


def _download_receipt(
    directory: Path,
    family: str,
    expected: dict[str, list[str]] | None,
) -> dict[str, Any]:
    suffix = ".deb" if family == "apt" else ".rpm"
    try:
        entries = list(os.scandir(directory))
    except OSError as error:
        raise ClientValidationError(f"cannot inspect {family} client output: {error}") from error
    files: list[Path] = []
    for entry in entries:
        try:
            metadata = entry.stat(follow_symlinks=False)
        except OSError as error:
            raise ClientValidationError(
                f"cannot inspect {family} client output {entry.name}: {error}"
            ) from error
        _require(stat.S_ISREG(metadata.st_mode) and not entry.is_symlink(),
                 f"{family} client output must contain only regular files")
        _require(entry.name.endswith(suffix),
                 f"{family} client output contains an unexpected file")
        files.append(Path(entry.path))
    _require(len(files) == 1, f"{family} client must download exactly one package")
    package = files[0]
    data = _read_regular(package, f"downloaded {family} package", MAX_DOWNLOAD_BYTES)
    digest = _sha256(data)
    versions: list[str] = []
    if expected is not None:
        _require(digest in expected,
                 f"downloaded {family} package is absent from the indexed snapshot")
        versions = sorted(expected[digest])
    return {
        "filename": package.name,
        "sha256": digest,
        "size": len(data),
        "snapshot_versions": versions,
    }



def _installed_cli_command(image, package, output, certificate, family, identity, digest):
    # This is a distinct installation sandbox. Download/signing sandboxes keep
    # their existing read-only, never-execute-product contract.
    suffix = "deb" if family == "apt" else "rpm"
    command = ["docker", "run", "--rm", "--platform", "linux/amd64",
               "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
               "--pids-limit", "128", "--memory", "768m", "--cpus", "2",
               "--ulimit", "fsize=134217728:134217728",
               "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=256m"]
    for capability in ("CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "SETGID", "SETUID", "SETFCAP"):
        command.extend(("--cap-add", capability))
    command.extend((
        "--volume", f"{package.resolve(strict=True)}:/candidate.{suffix}:ro",
        "--volume", f"{output.resolve(strict=True)}:/evidence:rw",
        "--volume", f"{certificate.resolve(strict=True)}:/reviewed-key.asc:ro",
        "--volume", f"{SCRIPT_DIR / 'validate-installed-wkcli.py'}:/acceptance.py:ro",
        "--volume", f"{SCRIPT_DIR.parent / 'tests/fixtures/wkcli-acceptance'}:/fixtures:ro",
        "--env", f"WK_EXPECTED_VERSION={identity['version']}",
        "--env", f"WK_EXPECTED_COMMIT={identity['commit']}",
        "--env", f"WK_PACKAGE_SHA256={digest}",
    ))
    if family == "apt":
        install = """
printf '%s  %s\n' "$WK_PACKAGE_SHA256" /candidate.deb | sha256sum --check --strict
printf '#!/bin/sh\nexit 101\n' >/usr/sbin/policy-rc.d
chmod 0755 /usr/sbin/policy-rc.d
apt-get -o Acquire::Retries=3 update
DEBIAN_FRONTEND=noninteractive apt-get -o Acquire::Retries=3 install -y python3 /candidate.deb
"""
    else:
        install = """
printf '%s  %s\n' "$WK_PACKAGE_SHA256" /candidate.rpm | sha256sum --check --strict
rpm --import /reviewed-key.asc
rpm --checksig /candidate.rpm
dnf --setopt=localpkg_gpgcheck=1 --setopt=timeout=60 install -y python3 /candidate.rpm
"""
    command.extend((image, "timeout", "--kill-after=10s", "600s", "bash", "-euc", install + """
python3 /acceptance.py --fixtures /fixtures --version "$WK_EXPECTED_VERSION" \
  --commit "$WK_EXPECTED_COMMIT" >/evidence/installed-cli.json
"""))
    return command


def _installed_cli_receipt(path, identity):
    value, _ = _load_json(path, "installed CLI receipt")
    _require(value.get("schema") == "wukongim/installed-cli-acceptance/v1"
             and value.get("identity") == identity and value.get("verified") is True
             and value.get("checks") == ["version", "help", "bench_validate", "db_query",
                                        "migrate_diagnose", "invalid_inputs", "read_only_data"],
             "installed CLI acceptance receipt is incomplete or has a wrong release identity")
    fixture_manifest, _ = _load_json(
        SCRIPT_DIR.parent / "tests/fixtures/wkcli-acceptance/sha256.json", "CLI fixture inventory")
    _require(value.get("fixtures") == fixture_manifest, "installed CLI fixture identity mismatch")
    return value

STABLE_RECEIPT_SCHEMA = "wukongim/stable-package-client-validation/v1"
TEST_PRODUCT_SHA = "fc18221282f289088ec9b4282fda8e0bfc67a3b8"


def stable_client_command(image, downloads, certificate, family, bootstrap):
    """Use the real stable bootstrap's sources and mirrorlist without a baseurl fallback."""
    _require(family in {"apt", "rpm"} and bootstrap is not None,
             "stable client requires its family bootstrap")
    command = _client_command(image, downloads, certificate, family, None,
                              COMPOSER.REPOSITORY_ORIGIN, bootstrap)
    script = command[-1].replace("preview", "stable")
    if family == "apt":
        script = script.replace("wukongim-archive-keyring", "wukongim-archive-keyring-stable")
    else:
        script = script.replace("wukongim-release", "wukongim-release-stable")
        start = script.index('  if ! grep -Fxq "baseurl=$WK_REPOSITORY_URL"')
        end = script.index("\ndnf_options=(", start)
        script = (script[:start] +
                  '  grep -Fxq "mirrorlist=$WK_REPOSITORY_URL" /tmp/repos.d/wukongim-stable.repo\n'
                  'else\n  echo "stable bootstrap is required" >&2\n  exit 1\nfi' + script[end:])
        script = script.replace("baseurl=https://packages.githubim.com/rpm/stable/el/9/x86_64",
                                "mirrorlist=https://packages.githubim.com/rpm/stable/el/9/x86_64/mirrorlist")
        # A CA override is supplied only by the isolated TEST ONLY runner. Normal
        # HTTPS clients use the image's ordinary trust store and keep sslverify=1.
        script = script.replace('dnf "${dnf_options[@]}" makecache',
                                'if [[ -n "${WK_TEST_CA_BUNDLE:-}" ]]; then\n'
                                '  test "${WK_TEST_ONLY_CLIENT:-}" = true\n'
                                '  dnf_options+=(--setopt=wukongim-stable.sslcacert="$WK_TEST_CA_BUNDLE")\n'
                                'fi\ndnf "${dnf_options[@]}" makecache')
    script = 'test -n "${WK_BOOTSTRAP_PACKAGE:-}"\n' + script
    for i, value in enumerate(command[:-1]):
        if value.endswith(f":/keys/{family}-preview.asc:ro"):
            command[i] = value.replace(f":/keys/{family}-preview.asc:ro", f":/keys/{family}-stable.asc:ro")
        if value.startswith("WK_REPOSITORY_URL=") and family == "rpm":
            command[i] = "WK_REPOSITORY_URL=https://packages.githubim.com/rpm/stable/el/9/x86_64/mirrorlist"
    command[-1] = script
    command[-3:-3] = ['timeout', '--signal=TERM', '--kill-after=10s', '300s']
    return command


def _validate_stable_clients(*, site_root, snapshot_path, base_url, transport_inputs,
                             apt_public_cert, rpm_public_cert, expected_version,
                             verify_installed_cli, test_only, test_product_sha, runner, fetcher):
    """Authenticate explicit stable inputs, then exercise the four actual package clients."""
    _require(transport_inputs is not None, "stable requires transport inputs")
    _require(site_root is None and snapshot_path is None,
             "stable derives its snapshot from transport inputs")
    _require(base_url == COMPOSER.REPOSITORY_ORIGIN,
             "stable clients require the canonical HTTPS origin")
    _require(isinstance(expected_version, str) and RELEASE_VERSION_RE.fullmatch(expected_version),
             "stable target must be strict release SemVer")
    _require(test_only or test_product_sha is None,
             "TEST ONLY product identity requires explicit test mode")
    _require(not test_only or test_product_sha == TEST_PRODUCT_SHA,
             "TEST ONLY clients require the fixed fixture product SHA")
    _require(verify_installed_cli, "stable requires installed CLI acceptance")
    tools = ("gpg", "gpgv", "rpm", "rpmkeys", "dpkg-deb", "zstd")
    missing = [name for name in tools if shutil.which(name) is None]
    _require(not missing, "stable native verifier tools are unavailable: " + ",".join(missing))
    planner = COMPOSER.local_module("plan-object-storage-publication.py")
    try:
        tool_versions = {}
        for name in tools:
            result = subprocess.run([name, '--version'], stdin=subprocess.DEVNULL,
                                    capture_output=True, timeout=10)
            _require(result.returncode == 0 and len(result.stdout) <= 16384 and len(result.stderr) <= 16384,
                     'stable native verifier version query failed: ' + name)
            tool_versions[name] = {'path': shutil.which(name),
                                   'version': result.stdout.decode(errors='replace').strip()}
        input_bytes = _read_regular(transport_inputs, "transport inputs", MAX_CONTROL_BYTES)
        inputs = planner.composite_inputs(transport_inputs, test_only)
        planned = planner.make_composite_plan(transport_inputs, test_only)
        _require(_read_regular(transport_inputs, "transport inputs", MAX_CONTROL_BYTES) == input_bytes,
                 "transport inputs changed during verification")
        descriptor = inputs["current"]["stable"]
        identity = next(item for item in planned["snapshots"]
                        if item["channel"] == "stable" and item["audit_release_id"] == descriptor["audit_release_id"])
        _require(identity["candidate"] == descriptor["candidate"], "stable candidate differs from transport input")
        candidate = identity["candidate"]
        _require(candidate["version"] == expected_version, "stable candidate differs from exact target")
        source_sha = test_product_sha if test_only else candidate["source"]["source_sha"]
        _require(isinstance(source_sha, str) and SHA1_RE.fullmatch(source_sha),
                 "stable target source SHA is invalid")
        archive = planner.local_path(descriptor["archive"], transport_inputs.parent, "stable archive")
        prefix = f"snapshots/stable/r{identity['audit_release_id']}-{candidate['snapshot_sha256']}"
        mirror_path = "/rpm/stable/el/9/x86_64/mirrorlist"
        mirror_route = next(item for item in planned["entrypoints"] if item["public_uri"] == mirror_path)
        mirror = next(item for item in planned["generated_objects"]
                      if item["object_key"] == mirror_route["object_key"])["content_utf8"].encode()
        origin = COMPOSER.REPOSITORY_ORIGIN
        _require(mirror == (origin + "/" + prefix + "/site/rpm/stable/el/9/x86_64/\n").encode(),
                 "stable mirrorlist differs from candidate snapshot")
        with tempfile.TemporaryDirectory(prefix="wk-stable-clients-") as temporary:
            root = Path(temporary)
            extracted = root / "snapshot"
            extracted_facts = planner.R.snapshot_archive.extract_snapshot(
                archive_path=archive, output_dir=extracted,
                max_members=planner.R.snapshot_archive.DEFAULT_MAX_MEMBERS,
                max_total_size=planner.R.ARCHIVE_MAX_TOTAL_BYTES)
            _require(extracted_facts["sha256"] == candidate["archive"]["sha256"]
                     and extracted_facts["size"] == candidate["archive"]["size"],
                     "stable archive changed during client preparation")
            snapshot_bytes = _read_regular(extracted / "audit/snapshot.json", "stable snapshot", MAX_CONTROL_BYTES)
            _require(_sha256(snapshot_bytes) == candidate["snapshot_sha256"], "stable snapshot digest differs")
            snapshot = json.loads(snapshot_bytes, object_pairs_hook=_reject_duplicate_pairs)
            bootstrap_bytes = _read_regular(extracted / "site/bootstrap/manifest.json", "stable bootstrap", MAX_CONTROL_BYTES)
            bootstrap = COMPOSER.validate_bootstrap_inventory(
                json.loads(bootstrap_bytes, object_pairs_hook=_reject_duplicate_pairs), prepared=False, channel="stable")
            certs = {"apt": apt_public_cert, "rpm": rpm_public_cert}
            for family, path in certs.items():
                public = _read_regular(path, family + " public certificate", MAX_CERTIFICATE_BYTES)
                archived = _read_regular(extracted / f"site/keys/{family}-stable.asc", "archived certificate", MAX_CERTIFICATE_BYTES)
                _require(public == archived, "stable reviewed public certificate differs")
                _require(fetcher(origin + f"/keys/{family}-stable.asc", MAX_CERTIFICATE_BYTES) == public,
                         "stable public endpoint certificate differs")
            _require(fetcher(origin + "/" + prefix + "/site/bootstrap/manifest.json", MAX_CONTROL_BYTES) == bootstrap_bytes,
                     "stable public bootstrap inventory differs")
            inrelease = _read_regular(extracted / "site/apt/dists/stable/InRelease", "stable InRelease", MAX_CONTROL_BYTES)
            _require(fetcher(origin + "/apt/dists/stable/InRelease", MAX_CONTROL_BYTES) == inrelease,
                     "stable public InRelease differs from exact target")
            _require(fetcher(origin + mirror_path, MAX_CONTROL_BYTES) == mirror,
                     "stable public mirrorlist differs from exact target")
            results = {"apt": [], "rpm": []}
            for family, clients in (("apt", APT_CLIENTS), ("rpm", RPM_CLIENTS)):
                package = bootstrap["packages"][family]
                raw = _read_regular(extracted / "site" / package["repository_path"], "stable bootstrap package", MAX_BOOTSTRAP_BYTES)
                _require(len(raw) == package["published_size"] and _sha256(raw) == package["published_sha256"],
                         "stable bootstrap package differs from inventory")
                for suffix in (package["download_path"], prefix + "/site/" + package["repository_path"]):
                    _require(fetcher(origin + "/" + suffix, MAX_BOOTSTRAP_BYTES) == raw,
                             "stable public bootstrap package differs")
                expected = {item["published_sha256"]: [item["version"]]
                            for item in snapshot["payloads"][family] if item["indexed"]}
                _require(len(expected) == 1 and next(iter(expected.values())) == [expected_version],
                         "stable indexed target differs")
                for distribution, image in clients:
                    downloads = root / distribution
                    downloads.mkdir(mode=0o777); downloads.chmod(0o777)
                    command = stable_client_command(image, downloads, certs[family], family,
                                                    extracted / "site" / package["repository_path"])
                    runner(command)
                    downloaded = _download_receipt(downloads, family, expected)
                    _require(downloaded["snapshot_versions"] == [expected_version], "stable downloaded target differs")
                    installed = None
                    if verify_installed_cli:
                        evidence = root / ("installed-" + distribution)
                        evidence.mkdir(mode=0o777); evidence.chmod(0o777)
                        expected_identity = {"version": expected_version, "commit": source_sha, "build_source": "release"}
                        runner(_installed_cli_command(image, downloads / downloaded["filename"], evidence,
                                                      certs[family], family, expected_identity, downloaded["sha256"]))
                        installed = _installed_cli_receipt(evidence / "installed-cli.json", expected_identity)
                    results[family].append({"distribution": distribution, "image": image,
                                            "bootstrap_installed": True, "download": downloaded, "installed_cli": installed})
            _require(fetcher(origin + "/apt/dists/stable/InRelease", MAX_CONTROL_BYTES) == inrelease
                     and fetcher(origin + mirror_path, MAX_CONTROL_BYTES) == mirror,
                     "stable public entrypoints changed during client validation")
        _require(_read_regular(transport_inputs, "transport inputs", MAX_CONTROL_BYTES) == input_bytes,
                 "transport inputs changed during client validation")
        return {"schema": STABLE_RECEIPT_SCHEMA, "channel": "stable", "test_only": test_only,
                "base_url": origin, "expected_version": expected_version, "expected_source_sha": source_sha,
                "expected_version_verified": True, "installed_cli_verified": verify_installed_cli,
                "bootstrap_verified": True, "bootstrap_version": bootstrap["version"],
                "repository_entrypoint_executed": False, "audit_release_id": identity["audit_release_id"],
                "snapshot_sha256": candidate["snapshot_sha256"], "transport_inputs_sha256": _sha256(input_bytes),
                "transport_plan_sha256": _sha256(_canonical_json(planned)),
                "status_revalidated": False, "stable_entrypoints_revalidated": True,
                "github_provenance_verified": False, "publication_verified": False,
                "native_verifier_tools": tool_versions, **results}
    except ClientValidationError:
        raise
    except (RuntimeError, ValueError, OSError, KeyError, TypeError, StopIteration, subprocess.SubprocessError) as error:
        raise ClientValidationError("stable client verification failed: " + str(error)) from error


def validate_clients(
    *,
    site_root: Path | None,
    snapshot_path: Path | None,
    base_url: str | None,
    apt_public_cert: Path,
    rpm_public_cert: Path,
    expected_version: str | None = None,
    verify_installed_cli: bool = False,
    channel: str = "preview",
    transport_inputs: Path | None = None,
    test_only: bool = False,
    test_product_sha: str | None = None,
    runner: Callable[[Sequence[str]], None] = _run,
    fetcher: Callable[[str, int], bytes] = _fetch_public_file,
) -> dict[str, Any]:
    _require(channel in {"preview", "stable"}, "unsupported client channel")
    if channel == "stable":
        return _validate_stable_clients(
            site_root=site_root, snapshot_path=snapshot_path, base_url=base_url,
            transport_inputs=transport_inputs, apt_public_cert=apt_public_cert,
            rpm_public_cert=rpm_public_cert, expected_version=expected_version,
            verify_installed_cli=verify_installed_cli, test_only=test_only,
            test_product_sha=test_product_sha, runner=runner, fetcher=fetcher)
    _require(transport_inputs is None and not test_only and test_product_sha is None,
             "stable-only options require explicit stable mode")
    local = site_root is not None
    _require(local != (base_url is not None),
             "exactly one of site root or base URL is required")
    _require(not local or snapshot_path is not None,
             "snapshot is required for local validation")
    if expected_version is not None:
        _require(isinstance(expected_version, str)
                 and VERSION_RE.fullmatch(expected_version) is not None,
                 "expected version must be strict prerelease SemVer")
        _require(snapshot_path is not None,
                 "expected version validation requires a reviewed snapshot")

    _require(not verify_installed_cli or (not local and snapshot_path is not None and expected_version is not None),
             "installed CLI acceptance requires remote mode, reviewed snapshot and exact version")
    certificates = {
        "apt": (
            apt_public_cert,
            _read_regular(apt_public_cert, "reviewed APT public certificate",
                          MAX_CERTIFICATE_BYTES),
        ),
        "rpm": (
            rpm_public_cert,
            _read_regular(rpm_public_cert, "reviewed RPM public certificate",
                          MAX_CERTIFICATE_BYTES),
        ),
    }
    expected: dict[str, dict[str, list[str]]] | None = None
    snapshot_sha256: str | None = None
    status: dict[str, Any] | None = None
    bootstrap: dict[str, Any] | None = None
    initial_status_bytes: bytes | None = None
    resolved_site: Path | None = None
    if local:
        assert site_root is not None and snapshot_path is not None
        resolved_site, expected, snapshot_sha256, identity = _validate_local_inputs(
            site_root, snapshot_path, certificates
        )
        bootstrap = _validate_local_bootstrap(
            resolved_site, identity, certificates["rpm"][1]
        )
        normalized_base_url = None
    else:
        assert base_url is not None
        normalized_base_url = _validated_base_url(base_url)
        status, initial_status_bytes = _validate_remote_inputs(
            normalized_base_url, certificates, fetcher
        )
        if snapshot_path is not None:
            expected, snapshot_sha256 = _validate_reviewed_remote_snapshot(
                snapshot_path,
                status,
                certificates,
                normalized_base_url,
                fetcher,
            )
        bootstrap = _validate_remote_bootstrap(
            normalized_base_url,
            (status["audit_release_id"], status["control_sha"]),
            certificates["rpm"][1],
            fetcher,
        )

    installed_identity = None
    if verify_installed_cli:
        reviewed, _ = _load_canonical_snapshot(snapshot_path)
        matches = [release for release in reviewed["releases"] if release["version"] == expected_version]
        _require(len(matches) == 1, "installed CLI target identity is ambiguous")
        installed_identity = {"version": expected_version, "commit": matches[0]["source_sha"],
                              "build_source": "release"}
    results: dict[str, list[dict[str, Any]]] = {"apt": [], "rpm": []}
    entrypoint_execution: dict[str, dict[str, Any]] = {}
    if expected_version is not None:
        assert expected is not None
        for family in ("apt", "rpm"):
            matches = [
                digest for digest, versions in expected[family].items()
                if versions == [expected_version]
            ]
            _require(len(matches) == 1,
                     f"indexed {family} snapshot does not contain exactly expected version {expected_version}")
    with tempfile.TemporaryDirectory(prefix="wukongim-package-clients-") as temporary:
        root = Path(temporary)
        bootstrap_paths: dict[str, Path] = {}
        if bootstrap is not None:
            bootstrap_root = root / "bootstrap"
            bootstrap_root.mkdir(mode=0o700)
            for family in ("apt", "rpm"):
                filename = bootstrap["manifest"]["packages"][family]["filename"]
                package = bootstrap_root / filename
                package.write_bytes(bootstrap["payloads"][family])
                package.chmod(0o444)
                bootstrap_paths[family] = package
        if (
            bootstrap is not None
            and bootstrap["repository_entrypoint_bytes"] is not None
        ):
            entrypoint_path = root / "repo"
            entrypoint_path.write_bytes(bootstrap["repository_entrypoint_bytes"])
            entrypoint_path.chmod(0o444)
            curl_shim = root / "curl"
            curl_shim.write_bytes(
                _repository_entrypoint_curl_shim(bootstrap["manifest"])
            )
            curl_shim.chmod(0o555)
            for family, (distribution, image) in (
                ("apt", APT_CLIENTS[0]),
                ("rpm", RPM_CLIENTS[0]),
            ):
                smoke_output = root / f"repository-entrypoint-{family}"
                smoke_output.mkdir(mode=0o700)
                smoke_output.chmod(0o777)
                command = _repository_entrypoint_command(
                    image,
                    smoke_output,
                    family,
                    entrypoint_path,
                    curl_shim,
                    bootstrap_paths[family],
                    certificates["rpm"][0],
                )
                try:
                    runner(command)
                except (OSError, subprocess.CalledProcessError) as error:
                    raise ClientValidationError(
                        f"{distribution} repository setup entrypoint failed: {error}"
                    ) from error
                entrypoint_execution[family] = {
                    "distribution": distribution,
                    "image": image,
                    "executions": 2,
                    "idempotent": True,
                    "product_absent": True,
                }
        for family, clients in (("apt", APT_CLIENTS), ("rpm", RPM_CLIENTS)):
            for distribution, image in clients:
                downloads = root / distribution
                downloads.mkdir(mode=0o700)
                # The hardened clients run as container root with every Linux
                # capability dropped.  A fresh bind-mounted output directory
                # therefore needs ordinary DAC write permission; its contents
                # are untrusted and fully revalidated immediately afterwards.
                downloads.chmod(0o777)
                command = _client_command(
                    image,
                    downloads,
                    certificates[family][0],
                    family,
                    resolved_site,
                    normalized_base_url,
                    bootstrap_paths.get(family),
                )
                try:
                    runner(command)
                except (OSError, subprocess.CalledProcessError) as error:
                    raise ClientValidationError(
                        f"{distribution} clean {family.upper()} client failed: {error}"
                    ) from error
                family_expected = expected[family] if expected is not None else None
                receipt = _download_receipt(downloads, family, family_expected)
                if expected_version is not None:
                    _require(
                        receipt["snapshot_versions"] == [expected_version],
                        f"{distribution} downloaded a version other than expected {expected_version}",
                    )
                installed = None
                if installed_identity is not None:
                    evidence = root / f"installed-{distribution}"
                    evidence.mkdir(mode=0o777)
                    evidence.chmod(0o777)
                    command = _installed_cli_command(
                        image, downloads / receipt["filename"], evidence,
                        certificates[family][0], family, installed_identity, receipt["sha256"],
                    )
                    try:
                        runner(command)
                    except (OSError, subprocess.CalledProcessError) as error:
                        raise ClientValidationError(f"{distribution} installed CLI acceptance failed: {error}") from error
                    installed = _installed_cli_receipt(evidence / "installed-cli.json", installed_identity)
                results[family].append({
                    "distribution": distribution,
                    "image": image,
                    "bootstrap_installed": bootstrap is not None,
                    "download": receipt,
                    "installed_cli": installed,
                })

    if not local:
        assert normalized_base_url is not None and initial_status_bytes is not None
        final_status_bytes = fetcher(
            f"{normalized_base_url}/status.json", MAX_CONTROL_BYTES
        )
        _require(final_status_bytes == initial_status_bytes,
                 "remote status.json changed during client validation")

    output: dict[str, Any] = {
        "schema": RECEIPT_SCHEMA,
        "mode": "local" if local else "remote",
        "installed_cli_verified": verify_installed_cli,
        "expected_version": expected_version,
        "expected_version_verified": expected_version is not None,
        "bootstrap_verified": bootstrap is not None,
        "repository_entrypoint_executed": set(entrypoint_execution) == {"apt", "rpm"},
        "entrypoint_execution": entrypoint_execution,
        "apt": results["apt"],
        "rpm": results["rpm"],
    }
    if bootstrap is None:
        output["bootstrap"] = None
    else:
        manifest = bootstrap["manifest"]
        output["bootstrap"] = {
            "schema": manifest["schema"],
            "version": manifest["version"],
            "manifest_sha256": bootstrap["manifest_sha256"],
            "packages": manifest["packages"],
            "repository_entrypoint": bootstrap["repository_entrypoint"],
        }
    if local:
        output["snapshot_sha256"] = snapshot_sha256
    else:
        assert status is not None and normalized_base_url is not None
        output["base_url"] = normalized_base_url
        output["audit_release_id"] = status["audit_release_id"]
        output["control_sha"] = status["control_sha"]
        output["snapshot_sha256"] = status["snapshot_sha256"]
        output["snapshot_verified"] = snapshot_path is not None
        output["status_revalidated"] = True
    return output


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    location = parser.add_mutually_exclusive_group(required=True)
    location.add_argument("--site-root", type=Path)
    location.add_argument("--base-url")
    parser.add_argument("--snapshot", type=Path)
    parser.add_argument("--channel", choices=("preview", "stable"), default="preview")
    parser.add_argument("--transport-inputs", type=Path)
    parser.add_argument("--test-only", action="store_true")
    parser.add_argument("--test-product-sha")
    parser.add_argument("--expected-version")
    parser.add_argument("--verify-installed-cli", action="store_true")
    parser.add_argument("--apt-public-cert", required=True, type=Path)
    parser.add_argument("--rpm-public-cert", required=True, type=Path)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        receipt = validate_clients(
            channel=args.channel, transport_inputs=args.transport_inputs,
            test_only=args.test_only, test_product_sha=args.test_product_sha,
            site_root=args.site_root,
            snapshot_path=args.snapshot,
            base_url=args.base_url,
            apt_public_cert=args.apt_public_cert,
            rpm_public_cert=args.rpm_public_cert,
            expected_version=args.expected_version,
            verify_installed_cli=args.verify_installed_cli,
        )
    except ClientValidationError as error:
        print(f"production package client validation failed: {error}", file=sys.stderr)
        return 1
    print(json.dumps(receipt, sort_keys=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
