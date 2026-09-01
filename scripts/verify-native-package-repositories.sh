#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: verify-native-package-repositories.sh \
  --repository DIR --apt-release RELATIVE_PATH \
  [--allow-test-only] \
  [--minimum-valid-days DAYS] \
  --apt-public-key FILE --apt-primary-fingerprint FPR \
  --apt-signing-fingerprint FPR \
  --rpm-repository RELATIVE_PATH --rpm-public-key FILE \
  --rpm-primary-fingerprint FPR --rpm-signing-fingerprint FPR

Verify exact APT/RPM signing subkeys and the full Release -> Packages -> deb and
repomd.xml -> metadata -> rpm digest closures. Test-only certificates are
rejected unless --allow-test-only is explicit. No host trust database is
modified.
EOF
}

repository=""; apt_release=""; allow_test_only="false"; minimum_valid_days="30"; apt_public_key=""; apt_primary=""; apt_signing=""
rpm_repository=""; rpm_public_key=""; rpm_primary=""; rpm_signing=""
while (($# > 0)); do
  case "$1" in
    --repository) (($# >= 2)) || { usage; exit 64; }; repository="$2"; shift 2 ;;
    --apt-release) (($# >= 2)) || { usage; exit 64; }; apt_release="$2"; shift 2 ;;
    --allow-test-only) allow_test_only="true"; shift ;;
    --minimum-valid-days) (($# >= 2)) || { usage; exit 64; }; minimum_valid_days="$2"; shift 2 ;;
    --apt-public-key) (($# >= 2)) || { usage; exit 64; }; apt_public_key="$2"; shift 2 ;;
    --apt-primary-fingerprint) (($# >= 2)) || { usage; exit 64; }; apt_primary="${2^^}"; shift 2 ;;
    --apt-signing-fingerprint) (($# >= 2)) || { usage; exit 64; }; apt_signing="${2^^}"; shift 2 ;;
    --rpm-repository) (($# >= 2)) || { usage; exit 64; }; rpm_repository="$2"; shift 2 ;;
    --rpm-public-key) (($# >= 2)) || { usage; exit 64; }; rpm_public_key="$2"; shift 2 ;;
    --rpm-primary-fingerprint) (($# >= 2)) || { usage; exit 64; }; rpm_primary="${2^^}"; shift 2 ;;
    --rpm-signing-fingerprint) (($# >= 2)) || { usage; exit 64; }; rpm_signing="${2^^}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 64 ;;
  esac
done

for value_name in repository apt_release apt_public_key apt_primary apt_signing rpm_repository rpm_public_key rpm_primary rpm_signing; do
  [[ -n "${!value_name}" ]] || { echo "missing required argument for ${value_name//_/-}" >&2; usage; exit 64; }
done
[[ -d "$repository" && ! -L "$repository" ]] || { echo "repository must be a non-symbolic-link directory: $repository" >&2; exit 66; }
[[ -f "$apt_public_key" && ! -L "$apt_public_key" ]] || { echo "APT public key must be a regular non-symbolic-link file" >&2; exit 66; }
[[ -f "$rpm_public_key" && ! -L "$rpm_public_key" ]] || { echo "RPM public key must be a regular non-symbolic-link file" >&2; exit 66; }
for fingerprint in "$apt_primary" "$apt_signing" "$rpm_primary" "$rpm_signing"; do
  [[ "$fingerprint" =~ ^[0-9A-F]{40}$ ]] || { echo "fingerprints must be complete 40-hex values" >&2; exit 64; }
done
[[ "$minimum_valid_days" =~ ^[0-9]+$ && "$minimum_valid_days" -le 3650 ]] || {
  echo "minimum-valid-days must be an integer from 0 through 3650" >&2
  exit 64
}
if [[ "$minimum_valid_days" == 0 && "$allow_test_only" != true ]]; then
  echo "a zero-day validity floor requires --allow-test-only" >&2
  exit 64
fi
for path in "$apt_release" "$rpm_repository"; do
  [[ "$path" != /* && "$path" != ".." && "$path" != ../* && "$path" != */../* && "$path" != */.. ]] || {
    echo "repository paths must be relative and must not contain '..': $path" >&2
    exit 64
  }
done
for tool in date gpg gpgv python3 rpm rpmkeys sha256sum stat zstd; do
  command -v "$tool" >/dev/null 2>&1 || { echo "$tool is required" >&2; exit 69; }
done
unsafe_repository_entry="$(find "$repository" ! -type d ! -type f -print -quit)"
if [[ -n "$unsafe_repository_entry" ]]; then
  echo "repository may contain only regular files and directories: $unsafe_repository_entry" >&2
  exit 65
fi
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
metadata_verifier="$script_dir/verify-native-package-metadata.py"
[[ -f "$metadata_verifier" && ! -L "$metadata_verifier" ]] || {
  echo "native-package metadata verifier is missing or unsafe" >&2
  exit 66
}

temporary_dir="$(mktemp -d)"
cleanup() { rm -rf -- "$temporary_dir"; }
trap cleanup EXIT HUP INT TERM
chmod 0700 "$temporary_dir"

import_public_key() {
  local role="$1" source="$2" primary="$3" signing="$4" keyring="$5"
  local home="$temporary_dir/${role,,}-gnupg" listing primary_count minimum_expiry
  minimum_expiry="$(( $(date +%s) + minimum_valid_days * 86400 ))"
  mkdir -m 0700 "$home"
  gpg --homedir "$home" --batch --quiet --no-default-keyring --keyring "$keyring" --import "$source"
  listing="$(gpg --homedir "$home" --batch --no-default-keyring --keyring "$keyring" \
    --with-colons --fingerprint --fingerprint)"
  primary_count="$(awk -F: '$1 == "pub" { count++ } END { print count + 0 }' <<<"$listing")"
  [[ "$primary_count" == 1 ]] || {
    echo "$role public key must contain exactly one primary certificate" >&2
    exit 65
  }
  awk -F: -v wanted="$primary" -v minimum_expiry="$minimum_expiry" '
    $1 == "pub" { valid = $2; expiry = $7; capabilities = $12; want_fpr = 1; next }
    want_fpr && $1 == "fpr" {
      exit !($10 == wanted && valid !~ /[re]/ &&
        capabilities ~ /c/ && capabilities !~ /[sea]/ &&
        (expiry == 0 || expiry >= minimum_expiry))
    }
    END { if (NR == 0) exit 1 }
  ' <<<"$listing" || {
    echo "$role primary certificate must match exactly, remain valid, and be certify-only" >&2
    exit 65
  }
  awk -F: -v wanted="$signing" -v minimum_expiry="$minimum_expiry" '
    $1 == "sub" { valid = $2; expiry = $7; capabilities = $12; want_fpr = 1; next }
    want_fpr && $1 == "fpr" {
      if (capabilities ~ /s/) {
        signing_count++
        if ($10 == wanted && valid !~ /[re]/ && capabilities !~ /[eca]/ &&
            (expiry == 0 || expiry >= minimum_expiry)) found = 1
      }
      want_fpr = 0
    }
    END { exit !(signing_count == 1 && found) }
  ' <<<"$listing" || {
    echo "$role public key must contain exactly the requested valid sign-only subkey" >&2
    exit 65
  }
  if [[ "$allow_test_only" != true ]]; then
    awk -F: '$1 == "uid" && toupper($10) ~ /TEST ONLY/ { found = 1 } END { exit found ? 0 : 1 }' <<<"$listing" && {
      echo "$role test-only certificate is forbidden without --allow-test-only" >&2
      exit 65
    }
  fi
}

apt_keyring="$temporary_dir/apt-keyring.gpg"
rpm_keyring="$temporary_dir/rpm-keyring.gpg"
import_public_key APT "$apt_public_key" "$apt_primary" "$apt_signing" "$apt_keyring"
import_public_key RPM "$rpm_public_key" "$rpm_primary" "$rpm_signing" "$rpm_keyring"

signing_manifest="$repository/signing-manifest.txt"
[[ -f "$signing_manifest" ]] || { echo "signed repository manifest is required" >&2; exit 66; }
for exact_line in \
  "APT_PRIMARY_FINGERPRINT=$apt_primary" \
  "APT_SIGNING_FINGERPRINT=$apt_signing" \
  "RPM_PRIMARY_FINGERPRINT=$rpm_primary" \
  "RPM_SIGNING_FINGERPRINT=$rpm_signing" \
  "APT_RELEASE=$apt_release" \
  "RPM_REPOSITORY=$rpm_repository"; do
  grep -Fxq "$exact_line" "$signing_manifest" || {
    echo "signed repository manifest does not match verification arguments" >&2
    exit 65
  }
done
if grep -Fxq 'TEST_ONLY=true' "$signing_manifest"; then
  [[ "$allow_test_only" == true ]] || { echo "test-only signed repository is forbidden" >&2; exit 65; }
elif ! grep -Fxq 'TEST_ONLY=false' "$signing_manifest"; then
  echo "signed repository manifest has an invalid TEST_ONLY value" >&2
  exit 65
fi

apt_release_path="$repository/$apt_release"
apt_directory="${apt_release_path%/*}"
rpm_repository_path="$repository/$rpm_repository"
[[ -f "$apt_release_path" && -f "$apt_directory/InRelease" && -f "$apt_release_path.gpg" ]] || {
  echo "APT Release, InRelease, and Release.gpg are all required" >&2
  exit 66
}
[[ -f "$rpm_repository_path/repodata/repomd.xml" && -f "$rpm_repository_path/repodata/repomd.xml.asc" ]] || {
  echo "RPM repomd.xml and repomd.xml.asc are required" >&2
  exit 66
}

verify_gpgv_signer() {
  local keyring="$1" expected="$2" status="$3"
  shift 3
  gpgv --status-fd 1 --keyring "$keyring" "$@" >"$status"
  grep -Eq "^\[GNUPG:\] VALIDSIG $expected( |$)" "$status" || {
    echo "signature did not use expected subkey $expected" >&2
    exit 65
  }
}

verify_gpgv_signer "$apt_keyring" "$apt_signing" "$temporary_dir/inrelease.status" \
  --output "$temporary_dir/inrelease-release" "$apt_directory/InRelease"
cmp -s "$temporary_dir/inrelease-release" "$apt_release_path" || {
  echo "InRelease cleartext differs from Release" >&2
  exit 65
}
grep -Fxq 'Acquire-By-Hash: yes' "$apt_release_path" || {
  echo "APT Release must enable Acquire-By-Hash" >&2
  exit 65
}
verify_gpgv_signer "$apt_keyring" "$apt_signing" "$temporary_dir/release-gpg.status" \
  "$apt_release_path.gpg" "$apt_release_path"

sha256_entries=0
while read -r expected_hash expected_size relative_path; do
  sha256_entries="$((sha256_entries + 1))"
  [[ "$expected_hash" =~ ^[0-9a-fA-F]{64}$ && "$expected_size" =~ ^[0-9]+$ ]] || {
    echo "invalid SHA256 entry in APT Release" >&2
    exit 65
  }
  [[ "$relative_path" != /* && "$relative_path" != ".." && "$relative_path" != ../* && "$relative_path" != */../* && "$relative_path" != */.. ]] || {
    echo "unsafe path in APT Release: $relative_path" >&2
    exit 65
  }
  target="$apt_directory/$relative_path"
  [[ -f "$target" ]] || { echo "APT Release target is missing: $relative_path" >&2; exit 65; }
  actual_hash="$(sha256sum "$target" | awk '{print $1}')"
  actual_size="$(stat -c %s "$target")"
  [[ "$actual_hash" == "${expected_hash,,}" && "$actual_size" == "$expected_size" ]] || {
    echo "APT Release digest mismatch: $relative_path" >&2
    exit 65
  }
  case "$relative_path" in
    */Packages|*/Packages.gz)
      by_hash="${target%/*}/by-hash/SHA256/${expected_hash,,}"
      [[ -f "$by_hash" ]] || { echo "APT by-hash target is missing: $relative_path" >&2; exit 65; }
      cmp -s "$target" "$by_hash" || { echo "APT by-hash target differs: $relative_path" >&2; exit 65; }
      ;;
  esac
done < <(awk '
  /^SHA256:$/ { in_sha256 = 1; next }
  /^[A-Za-z0-9-]+:$/ { in_sha256 = 0 }
  in_sha256 && NF == 3 { print $1, $2, $3 }
' "$apt_release_path")
((sha256_entries > 0)) || { echo "APT Release contains no SHA256 entries" >&2; exit 65; }

rpm_db="$temporary_dir/rpmdb"
mkdir -p "$rpm_db"
rpm --dbpath "$rpm_db" --initdb
rpm --dbpath "$rpm_db" --import "$rpm_public_key"
mapfile -d '' rpm_packages < <(find "$rpm_repository_path/Packages" -type f -name '*.rpm' -print0)
((${#rpm_packages[@]} > 0)) || { echo "no RPM packages found in repository" >&2; exit 66; }
for package in "${rpm_packages[@]}"; do
  rpmkeys --dbpath "$rpm_db" --checksig "$package" >/dev/null
  signature_file="$temporary_dir/rpm-signature.asc"
  rpm --dbpath "$rpm_db" -qp --queryformat '%{RSAHEADER:armor}\n' "$package" >"$signature_file"
  packet_listing="$(gpg --homedir "$temporary_dir/rpm-gnupg" --batch --list-packets "$signature_file" 2>&1)"
  grep -Fqi "issuer fpr v4 $rpm_signing" <<<"$packet_listing" || {
    echo "RPM package was not signed by exact subkey $rpm_signing: $package" >&2
    exit 65
  }
done
verify_gpgv_signer "$rpm_keyring" "$rpm_signing" "$temporary_dir/repomd.status" \
  "$rpm_repository_path/repodata/repomd.xml.asc" "$rpm_repository_path/repodata/repomd.xml"

python3 "$metadata_verifier" \
  --repository "$repository" \
  --apt-release "$apt_release" \
  --rpm-repository "$rpm_repository"

echo "native-package repository signatures and metadata closure validated"
