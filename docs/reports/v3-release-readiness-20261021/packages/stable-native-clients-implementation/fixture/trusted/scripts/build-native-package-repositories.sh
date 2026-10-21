#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: build-native-package-repositories.sh \
  --packages-dir DIR --output DIR \
  --apt-suite SUITE --apt-architecture ARCH \
  --rpm-channel CHANNEL --rpm-basearch ARCH

Build unsigned APT and RPM repository trees atomically. DIR must contain at
least one .deb and one .rpm. The output is staging data and is not publishable
until sign-native-package-repositories.sh succeeds.
EOF
}

packages_dir=""
output_dir=""
apt_suite=""
apt_architecture=""
rpm_channel=""
rpm_basearch=""
while (($# > 0)); do
  case "$1" in
    --packages-dir) (($# >= 2)) || { usage; exit 64; }; packages_dir="$2"; shift 2 ;;
    --output) (($# >= 2)) || { usage; exit 64; }; output_dir="$2"; shift 2 ;;
    --apt-suite) (($# >= 2)) || { usage; exit 64; }; apt_suite="$2"; shift 2 ;;
    --apt-architecture) (($# >= 2)) || { usage; exit 64; }; apt_architecture="$2"; shift 2 ;;
    --rpm-channel) (($# >= 2)) || { usage; exit 64; }; rpm_channel="$2"; shift 2 ;;
    --rpm-basearch) (($# >= 2)) || { usage; exit 64; }; rpm_basearch="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 64 ;;
  esac
done

for value_name in packages_dir output_dir apt_suite apt_architecture rpm_channel rpm_basearch; do
  [[ -n "${!value_name}" ]] || { echo "missing --${value_name//_/-}" >&2; usage; exit 64; }
done
[[ -d "$packages_dir" ]] || { echo "packages directory does not exist: $packages_dir" >&2; exit 66; }
[[ ! -L "$packages_dir" ]] || { echo "packages directory must not be a symbolic link" >&2; exit 65; }
[[ ! -e "$output_dir" ]] || { echo "output already exists: $output_dir" >&2; exit 73; }
[[ "$apt_suite" =~ ^[a-z0-9][a-z0-9.-]{0,63}$ ]] || { echo "invalid APT suite" >&2; exit 64; }
[[ "$apt_architecture" =~ ^[a-z0-9][a-z0-9_-]{0,31}$ ]] || { echo "invalid APT architecture" >&2; exit 64; }
[[ "$rpm_channel" =~ ^[a-z0-9][a-z0-9.-]{0,63}$ ]] || { echo "invalid RPM channel" >&2; exit 64; }
[[ "$rpm_basearch" =~ ^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$ ]] || { echo "invalid RPM base architecture" >&2; exit 64; }

for tool in dpkg-scanpackages apt-ftparchive createrepo_c gzip sha256sum; do
  command -v "$tool" >/dev/null 2>&1 || { echo "$tool is required" >&2; exit 69; }
done

shopt -s nullglob
deb_packages=("$packages_dir"/*.deb)
rpm_packages=("$packages_dir"/*.rpm)
((${#deb_packages[@]} > 0)) || { echo "no .deb packages found" >&2; exit 66; }
((${#rpm_packages[@]} > 0)) || { echo "no .rpm packages found" >&2; exit 66; }

output_parent="$(dirname "$output_dir")"
output_name="$(basename "$output_dir")"
mkdir -p "$output_parent"
output_parent="$(cd "$output_parent" && pwd -P)"
output_dir="$output_parent/$output_name"
stage="$(mktemp -d "$output_parent/.${output_name}.tmp.XXXXXX")"
cleanup() { rm -rf -- "$stage"; }
trap cleanup EXIT HUP INT TERM

apt_root="$stage/apt"
apt_pool="$apt_root/pool/main/w/wukongim"
apt_binary="$apt_root/dists/$apt_suite/main/binary-$apt_architecture"
rpm_root="$stage/rpm/$rpm_channel/el/9/$rpm_basearch"
mkdir -p "$apt_pool" "$apt_binary" "$rpm_root/Packages"

for package in "${deb_packages[@]}"; do
  [[ -f "$package" && ! -L "$package" ]] || { echo "packages must be regular files: $package" >&2; exit 65; }
  cp -- "$package" "$apt_pool/"
done
for package in "${rpm_packages[@]}"; do
  [[ -f "$package" && ! -L "$package" ]] || { echo "packages must be regular files: $package" >&2; exit 65; }
  cp -- "$package" "$rpm_root/Packages/"
done

(
  cd "$apt_root"
  dpkg-scanpackages --multiversion --arch "$apt_architecture" pool /dev/null \
    >"dists/$apt_suite/main/binary-$apt_architecture/Packages"
  gzip -9n -c "dists/$apt_suite/main/binary-$apt_architecture/Packages" \
    >"dists/$apt_suite/main/binary-$apt_architecture/Packages.gz"
  apt-ftparchive \
    -o "APT::FTPArchive::Release::Origin=WuKongIM" \
    -o "APT::FTPArchive::Release::Label=WuKongIM" \
    -o "APT::FTPArchive::Release::Suite=$apt_suite" \
    -o "APT::FTPArchive::Release::Codename=$apt_suite" \
    -o "APT::FTPArchive::Release::Architectures=$apt_architecture" \
    -o "APT::FTPArchive::Release::Components=main" \
    -o "APT::FTPArchive::Release::Acquire-By-Hash=yes" \
    release "dists/$apt_suite" >"Release.tmp"
  mv -- "Release.tmp" "dists/$apt_suite/Release"

  by_hash="dists/$apt_suite/main/binary-$apt_architecture/by-hash/SHA256"
  mkdir -p "$by_hash"
  for metadata in Packages Packages.gz; do
    metadata_path="dists/$apt_suite/main/binary-$apt_architecture/$metadata"
    digest="$(sha256sum "$metadata_path" | awk '{print $1}')"
    cp -- "$metadata_path" "$by_hash/$digest"
  done
)

createrepo_c --quiet --simple-md-filenames "$rpm_root"
cat >"$stage/repository-layout.txt" <<EOF
TEST_ONLY_UNSIGNED_STAGING=1
APT_RELEASE=apt/dists/$apt_suite/Release
RPM_REPOSITORY=rpm/$rpm_channel/el/9/$rpm_basearch
EOF

find "$stage" -type d -exec chmod 0755 {} +
find "$stage" -type f -exec chmod 0644 {} +

mv -- "$stage" "$output_dir"
trap - EXIT HUP INT TERM
echo "unsigned native-package repositories built at $output_dir"
