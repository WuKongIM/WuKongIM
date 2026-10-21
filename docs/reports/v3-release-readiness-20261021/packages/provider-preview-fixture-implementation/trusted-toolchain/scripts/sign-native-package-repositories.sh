#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: sign-native-package-repositories.sh \
  --input DIR --output DIR --gnupg-home DIR \
  [--test-only] \
  [--minimum-valid-days DAYS] \
  --apt-release RELATIVE_PATH \
  --apt-primary-fingerprint FPR --apt-signing-fingerprint FPR \
  --apt-passphrase-file FILE \
  --rpm-repository RELATIVE_PATH \
  --rpm-primary-fingerprint FPR --rpm-signing-fingerprint FPR \
  --rpm-passphrase-file FILE

Copy an unsigned staging tree, sign every RPM package, regenerate RPM metadata,
and sign APT and RPM metadata. All fingerprints must be full 40-hex values.
The input is never modified and no key is generated or selected implicitly.
The production validity floor defaults to 30 days. A zero-day floor requires
--test-only and both certificates must have a UID containing TEST ONLY.
EOF
}

input_dir=""; output_dir=""; gnupg_home=""; test_only="false"; minimum_valid_days="30"; apt_release=""; rpm_repository=""
apt_primary=""; apt_signing=""; apt_passphrase_file=""
rpm_primary=""; rpm_signing=""; rpm_passphrase_file=""
while (($# > 0)); do
  case "$1" in
    --input) (($# >= 2)) || { usage; exit 64; }; input_dir="$2"; shift 2 ;;
    --output) (($# >= 2)) || { usage; exit 64; }; output_dir="$2"; shift 2 ;;
    --gnupg-home) (($# >= 2)) || { usage; exit 64; }; gnupg_home="$2"; shift 2 ;;
    --test-only) test_only="true"; shift ;;
    --minimum-valid-days) (($# >= 2)) || { usage; exit 64; }; minimum_valid_days="$2"; shift 2 ;;
    --apt-release) (($# >= 2)) || { usage; exit 64; }; apt_release="$2"; shift 2 ;;
    --apt-primary-fingerprint) (($# >= 2)) || { usage; exit 64; }; apt_primary="${2^^}"; shift 2 ;;
    --apt-signing-fingerprint) (($# >= 2)) || { usage; exit 64; }; apt_signing="${2^^}"; shift 2 ;;
    --apt-passphrase-file) (($# >= 2)) || { usage; exit 64; }; apt_passphrase_file="$2"; shift 2 ;;
    --rpm-repository) (($# >= 2)) || { usage; exit 64; }; rpm_repository="$2"; shift 2 ;;
    --rpm-primary-fingerprint) (($# >= 2)) || { usage; exit 64; }; rpm_primary="${2^^}"; shift 2 ;;
    --rpm-signing-fingerprint) (($# >= 2)) || { usage; exit 64; }; rpm_signing="${2^^}"; shift 2 ;;
    --rpm-passphrase-file) (($# >= 2)) || { usage; exit 64; }; rpm_passphrase_file="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 64 ;;
  esac
done

for value_name in input_dir output_dir gnupg_home apt_release apt_primary apt_signing apt_passphrase_file rpm_repository rpm_primary rpm_signing rpm_passphrase_file; do
  [[ -n "${!value_name}" ]] || { echo "missing required argument for ${value_name//_/-}" >&2; usage; exit 64; }
done
[[ -d "$input_dir" ]] || { echo "input directory does not exist: $input_dir" >&2; exit 66; }
[[ ! -L "$input_dir" ]] || { echo "input directory must not be a symbolic link" >&2; exit 65; }
[[ -d "$gnupg_home" ]] || { echo "GNUPG home does not exist: $gnupg_home" >&2; exit 66; }
[[ ! -L "$gnupg_home" ]] || { echo "GNUPG home must not be a symbolic link" >&2; exit 65; }
[[ -f "$apt_passphrase_file" && ! -L "$apt_passphrase_file" ]] || { echo "APT passphrase must be a regular non-symbolic-link file" >&2; exit 66; }
[[ -f "$rpm_passphrase_file" && ! -L "$rpm_passphrase_file" ]] || { echo "RPM passphrase must be a regular non-symbolic-link file" >&2; exit 66; }
[[ ! -e "$output_dir" && ! -L "$output_dir" ]] || { echo "output already exists or is a symbolic link: $output_dir" >&2; exit 73; }

require_private_mode() {
  local role="$1" path="$2" mode owner
  mode="$(stat -c %a "$path")"
  owner="$(stat -c %u "$path")"
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] || { echo "cannot determine permissions for $role" >&2; exit 65; }
  [[ "$owner" == "$(id -u)" ]] || { echo "$role must be owned by the signing user" >&2; exit 65; }
  (( (8#$mode & 077) == 0 )) || { echo "$role must not be accessible by group or other" >&2; exit 65; }
}
require_private_mode "GNUPG home" "$gnupg_home"
require_private_mode "APT passphrase file" "$apt_passphrase_file"
require_private_mode "RPM passphrase file" "$rpm_passphrase_file"
[[ "$minimum_valid_days" =~ ^[0-9]+$ && "$minimum_valid_days" -le 3650 ]] || {
  echo "minimum-valid-days must be an integer from 0 through 3650" >&2
  exit 64
}
if [[ "$minimum_valid_days" == 0 && "$test_only" != true ]]; then
  echo "a zero-day validity floor requires --test-only" >&2
  exit 64
fi
for fingerprint in "$apt_primary" "$apt_signing" "$rpm_primary" "$rpm_signing"; do
  [[ "$fingerprint" =~ ^[0-9A-F]{40}$ ]] || { echo "fingerprints must be complete 40-hex values" >&2; exit 64; }
done
for path in "$apt_release" "$rpm_repository"; do
  [[ "$path" != /* && "$path" != ".." && "$path" != ../* && "$path" != */../* && "$path" != */.. ]] || {
    echo "repository paths must be relative and must not contain '..': $path" >&2
    exit 64
  }
done
for tool in gpg id realpath rpm rpmsign rpmkeys createrepo_c stat; do
  command -v "$tool" >/dev/null 2>&1 || { echo "$tool is required" >&2; exit 69; }
done

input_dir="$(realpath -e -- "$input_dir")"
output_dir="$(realpath -m -- "$output_dir")"
case "$output_dir/" in
  "$input_dir"/*)
    echo "output repository must not be located inside the input repository" >&2
    exit 65
    ;;
esac

validate_key() {
  local role="$1" primary="$2" signing="$3" passphrase_file="$4"
  local listing primary_count status_file probe_dir minimum_expiry
  minimum_expiry="$(( $(date +%s) + minimum_valid_days * 86400 ))"
  listing="$(gpg --homedir "$gnupg_home" --batch --with-colons --fingerprint --fingerprint "$primary")"
  primary_count="$(awk -F: '$1 == "pub" { count++ } END { print count + 0 }' <<<"$listing")"
  [[ "$primary_count" == 1 ]] || {
    echo "$role key selection must resolve to exactly one primary certificate" >&2
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
    END { exit !(found && signing_count == 1) }
  ' <<<"$listing" || {
    echo "$role certificate must have exactly the requested sign-only subkey, valid for at least $minimum_valid_days days" >&2
    exit 65
  }
  if [[ "$test_only" == true ]]; then
    awk -F: '$1 == "uid" && toupper($10) ~ /TEST ONLY/ { found = 1 } END { exit !found }' <<<"$listing" || {
      echo "$role --test-only certificate UID must contain TEST ONLY" >&2
      exit 65
    }
  else
    awk -F: '$1 == "uid" && toupper($10) ~ /TEST ONLY/ { found = 1 } END { exit found ? 0 : 1 }' <<<"$listing" && {
      echo "$role TEST ONLY certificate is forbidden for production signing" >&2
      exit 65
    }
  fi

  listing="$(gpg --homedir "$gnupg_home" --batch --with-colons --list-secret-keys "$primary")"
  awk -F: '$1 == "sec" { exit ($15 ~ /#/) ? 0 : 1 } END { if (NR == 0) exit 1 }' <<<"$listing" || {
    echo "$role primary secret material must not be present in the signing keyring" >&2
    exit 65
  }

  probe_dir="$(mktemp -d)"
  (
    trap 'rm -rf -- "$probe_dir"' EXIT HUP INT TERM
    status_file="$probe_dir/status"
    printf '%s\n' "native-package $role signing-key probe" >"$probe_dir/data"
    gpg --homedir "$gnupg_home" --batch --yes --pinentry-mode loopback \
      --passphrase-file "$passphrase_file" --local-user "$signing!" \
      --digest-algo SHA256 --output "$probe_dir/signature" --detach-sign "$probe_dir/data"
    gpg --homedir "$gnupg_home" --batch --status-fd 1 \
      --verify "$probe_dir/signature" "$probe_dir/data" >"$status_file" 2>/dev/null
    grep -Eq "^\[GNUPG:\] VALIDSIG $signing( |$)" "$status_file" || {
      echo "$role signing probe did not use the exact requested subkey" >&2
      exit 65
    }
  )
}

validate_key APT "$apt_primary" "$apt_signing" "$apt_passphrase_file"
validate_key RPM "$rpm_primary" "$rpm_signing" "$rpm_passphrase_file"

output_parent="$(dirname "$output_dir")"
output_name="$(basename "$output_dir")"
mkdir -p "$output_parent"
output_parent="$(cd "$output_parent" && pwd -P)"
output_dir="$output_parent/$output_name"
stage="$(mktemp -d "$output_parent/.${output_name}.tmp.XXXXXX")"
cleanup() { rm -rf -- "$stage"; }
trap cleanup EXIT HUP INT TERM
unsafe_input="$(find "$input_dir" ! -type d ! -type f -print -quit)"
if [[ -n "$unsafe_input" ]]; then
  echo "input repository may contain only regular files and directories: $unsafe_input" >&2
  exit 65
fi
cp -a -- "$input_dir"/. "$stage"/

apt_release_path="$stage/$apt_release"
apt_directory="${apt_release_path%/*}"
rpm_repository_path="$stage/$rpm_repository"
[[ -f "$apt_release_path" ]] || { echo "APT Release file not found: $apt_release" >&2; exit 66; }
[[ -d "$rpm_repository_path/Packages" ]] || { echo "RPM Packages directory not found: $rpm_repository" >&2; exit 66; }
unsupported_apt_index="$(find "$apt_directory" -name 'Packages*' ! -name 'Packages' ! -name 'Packages.gz' -print -quit)"
if [[ -n "$unsupported_apt_index" ]]; then
  echo "APT repository contains an unsupported Packages index: $unsupported_apt_index" >&2
  exit 65
fi
unsupported_release_index="$(awk '
  /^SHA256:$/ { in_sha256 = 1; next }
  /^[A-Za-z0-9-]+:$/ { in_sha256 = 0 }
  in_sha256 && NF == 3 {
    count = split($3, parts, "/")
    name = parts[count]
    if (name ~ /^Packages/ && name != "Packages" && name != "Packages.gz") {
      print $3
      exit
    }
  }
' "$apt_release_path")"
if [[ -n "$unsupported_release_index" ]]; then
  echo "APT Release authenticates an unsupported Packages index: $unsupported_release_index" >&2
  exit 65
fi

shopt -s nullglob
rpm_packages=("$rpm_repository_path"/Packages/*.rpm)
((${#rpm_packages[@]} > 0)) || { echo "no RPM packages found in repository" >&2; exit 66; }

mkdir -p "$stage/keys"
gpg --homedir "$gnupg_home" --batch --armor --export-options export-minimal \
  --output "$stage/keys/apt-signing-key.asc" --export "$apt_primary"
gpg --homedir "$gnupg_home" --batch --armor --export-options export-minimal \
  --output "$stage/keys/rpm-signing-key.asc" --export "$rpm_primary"

rpm_verify_db="$stage/.rpm-verify-db"
mkdir -p "$rpm_verify_db"
rpm --dbpath "$rpm_verify_db" --initdb
rpm --dbpath "$rpm_verify_db" --import "$stage/keys/rpm-signing-key.asc"

verify_exact_rpm_signature() {
  local package="$1" output packet_listing signature_file
  output="$(rpmkeys --dbpath "$rpm_verify_db" --verbose --checksig "$package")"
  grep -Eq 'Header V4 RSA/SHA(256|512) Signature, key ID [0-9a-fA-F]{8}: OK' <<<"$output" || {
    echo "RPM package signature verification failed: $package" >&2
    exit 65
  }
  signature_file="$stage/.rpm-signature.asc"
  rpm --dbpath "$rpm_verify_db" -qp --queryformat '%{RSAHEADER:armor}\n' "$package" >"$signature_file"
  packet_listing="$(gpg --homedir "$gnupg_home" --batch --list-packets "$signature_file" 2>&1)"
  rm -f -- "$signature_file"
  grep -Fqi "issuer fpr v4 $rpm_signing" <<<"$packet_listing" || {
    echo "RPM package was not signed by exact subkey $rpm_signing: $package" >&2
    exit 65
  }
}

# The signing-key probe above unlocks the exact RPM subkey in gpg-agent. RPM
# 4.16 invokes GnuPG itself; an unavailable passphrase cache therefore fails
# closed instead of opening an interactive fallback in this non-TTY script.
for package in "${rpm_packages[@]}"; do
  GNUPGHOME="$gnupg_home" rpmsign \
    --define "_gpg_name $rpm_signing!" \
    --define "_gpg_path $gnupg_home" \
    --define "__gpg $(command -v gpg)" \
    --addsign "$package"
  verify_exact_rpm_signature "$package"
done
rm -rf -- "$rpm_verify_db"

rm -rf -- "$rpm_repository_path/repodata"
createrepo_c --quiet --simple-md-filenames "$rpm_repository_path"

gpg --homedir "$gnupg_home" --batch --yes --pinentry-mode loopback \
  --passphrase-file "$apt_passphrase_file" --local-user "$apt_signing!" \
  --digest-algo SHA256 --output "$apt_directory/InRelease" --clearsign "$apt_release_path"
gpg --homedir "$gnupg_home" --batch --yes --pinentry-mode loopback \
  --passphrase-file "$apt_passphrase_file" --local-user "$apt_signing!" \
  --digest-algo SHA256 --armor --output "$apt_release_path.gpg" --detach-sign "$apt_release_path"
gpg --homedir "$gnupg_home" --batch --yes --pinentry-mode loopback \
  --passphrase-file "$rpm_passphrase_file" --local-user "$rpm_signing!" \
  --digest-algo SHA256 --armor \
  --output "$rpm_repository_path/repodata/repomd.xml.asc" \
  --detach-sign "$rpm_repository_path/repodata/repomd.xml"

rm -f -- "$stage/repository-layout.txt"
cat >"$stage/signing-manifest.txt" <<EOF
TEST_ONLY=$test_only
APT_PRIMARY_FINGERPRINT=$apt_primary
APT_SIGNING_FINGERPRINT=$apt_signing
RPM_PRIMARY_FINGERPRINT=$rpm_primary
RPM_SIGNING_FINGERPRINT=$rpm_signing
APT_RELEASE=$apt_release
RPM_REPOSITORY=$rpm_repository
EOF

find "$stage" -type d -exec chmod 0755 {} +
find "$stage" -type f -exec chmod 0644 {} +

mv -- "$stage" "$output_dir"
trap - EXIT HUP INT TERM
echo "signed native-package repositories created at $output_dir"
