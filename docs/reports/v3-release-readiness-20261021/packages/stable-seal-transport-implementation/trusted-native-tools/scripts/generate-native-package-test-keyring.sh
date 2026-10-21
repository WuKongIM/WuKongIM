#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: generate-native-package-test-keyring.sh --output DIR

Generate two ephemeral, one-day OpenPGP test certificates in DIR. DIR must
not already exist and must be outside the repository. The result is TEST ONLY
and contains unprotected signing subkeys for credential-free integration tests.
EOF
}

output_dir=""
while (($# > 0)); do
  case "$1" in
    --output)
      (($# >= 2)) || { usage; exit 64; }
      output_dir="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage
      exit 64
      ;;
  esac
done

[[ -n "$output_dir" ]] || { usage; exit 64; }
for tool in gpg gpgconf; do
  command -v "$tool" >/dev/null 2>&1 || { echo "$tool is required" >&2; exit 69; }
done
[[ ! -e "$output_dir" ]] || { echo "output already exists: $output_dir" >&2; exit 73; }

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
output_parent="$(dirname "$output_dir")"
output_name="$(basename "$output_dir")"
mkdir -p "$output_parent"
output_parent="$(cd "$output_parent" && pwd -P)"
output_dir="$output_parent/$output_name"
case "$output_dir" in
  "$repository_root"|"$repository_root"/*)
    echo "test secret keys must be generated outside the repository: $output_dir" >&2
    exit 64
    ;;
esac

stage="$(mktemp -d "$output_parent/.${output_name}.tmp.XXXXXX")"
cleanup() {
  rm -rf -- "$stage"
}
trap cleanup EXIT HUP INT TERM

generation_home="$stage/generation-gnupg"
signing_home="$stage/gnupg"
public_dir="$stage/public"
secret_dir="$stage/secret-transfer"
mkdir -m 0700 "$generation_home" "$signing_home" "$secret_dir"
mkdir -m 0700 "$public_dir"

generate_certificate() {
  local role="$1"
  local uid="$2"
  local public_path="$public_dir/$role.asc"
  local secret_path="$secret_dir/$role.gpg"
  local primary_fingerprint signing_fingerprint

  gpg --homedir "$generation_home" --batch --quiet --pinentry-mode loopback \
    --passphrase '' --quick-generate-key "$uid" rsa3072 cert 1d
  primary_fingerprint="$(
    gpg --homedir "$generation_home" --batch --with-colons --fingerprint "$uid" |
      awk -F: '$1 == "fpr" { print $10; exit }'
  )"
  [[ "$primary_fingerprint" =~ ^[0-9A-F]{40}$ ]] || {
    echo "failed to resolve $role primary fingerprint" >&2
    exit 70
  }

  gpg --homedir "$generation_home" --batch --quiet --pinentry-mode loopback \
    --passphrase '' --quick-add-key "$primary_fingerprint" rsa3072 sign 1d
  signing_fingerprint="$(
    gpg --homedir "$generation_home" --batch --with-colons --fingerprint "$primary_fingerprint" |
      awk -F: '$1 == "sub" { want = 1; next } want && $1 == "fpr" { print $10; exit }'
  )"
  [[ "$signing_fingerprint" =~ ^[0-9A-F]{40}$ ]] || {
    echo "failed to resolve $role signing fingerprint" >&2
    exit 70
  }

  gpg --homedir "$generation_home" --batch --armor \
    --output "$public_path" --export "$primary_fingerprint"
  gpg --homedir "$generation_home" --batch --pinentry-mode loopback --passphrase '' \
    --output "$secret_path" --export-secret-subkeys "$primary_fingerprint"
  gpg --homedir "$signing_home" --batch --quiet --import "$public_path"
  gpg --homedir "$signing_home" --batch --quiet --pinentry-mode loopback \
    --passphrase '' --import "$secret_path"

  printf '%s\t%s\n' "$primary_fingerprint" "$signing_fingerprint"
}

apt_fingerprints="$(generate_certificate apt 'WuKongIM Native Package APT TEST ONLY <test-only@invalid>')"
rpm_fingerprints="$(generate_certificate rpm 'WuKongIM Native Package RPM TEST ONLY <test-only@invalid>')"
read -r apt_primary apt_signing <<<"$apt_fingerprints"
read -r rpm_primary rpm_signing <<<"$rpm_fingerprints"

# Stop path-bound agents before the atomic directory rename. GnuPG 2.4 may
# otherwise leave keyboxd sockets that still refer to the temporary path.
gpgconf --homedir "$generation_home" --kill all
gpgconf --homedir "$signing_home" --kill all
rm -rf -- "$generation_home" "$secret_dir"
touch "$stage/apt-passphrase.txt" "$stage/rpm-passphrase.txt"
chmod 0600 "$stage/apt-passphrase.txt" "$stage/rpm-passphrase.txt"
chmod 0644 "$public_dir/apt.asc" "$public_dir/rpm.asc"

cat >"$stage/manifest.env" <<EOF
WK_NATIVE_PACKAGE_TEST_ONLY=1
WK_APT_PRIMARY_FINGERPRINT=$apt_primary
WK_APT_SIGNING_FINGERPRINT=$apt_signing
WK_RPM_PRIMARY_FINGERPRINT=$rpm_primary
WK_RPM_SIGNING_FINGERPRINT=$rpm_signing
EOF
chmod 0600 "$stage/manifest.env"

mv -- "$stage" "$output_dir"
trap - EXIT HUP INT TERM
echo "TEST ONLY native-package keyring generated at $output_dir"
