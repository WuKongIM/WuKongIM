#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 69; }
package_dir="${WK_NATIVE_PACKAGE_DIST_DIR:-$repository_root/dist}"
[[ -d "$package_dir" && ! -L "$package_dir" ]] || {
  echo "native package directory is missing or unsafe: $package_dir" >&2
  exit 66
}
package_dir="$(cd "$package_dir" && pwd -P)"

shopt -s nullglob
deb_packages=("$package_dir"/wukongim*.deb)
rpm_packages=("$package_dir"/wukongim*.rpm)
((${#deb_packages[@]} == 1)) || { echo "exactly one deb preview is required" >&2; exit 66; }
((${#rpm_packages[@]} == 1)) || { echo "exactly one rpm preview is required" >&2; exit 66; }

work_dir="$(mktemp -d)"
cleanup() {
  find "$work_dir" -depth -delete
}
trap cleanup EXIT HUP INT TERM
chmod 0755 "$work_dir"

docker run --rm \
  --platform linux/amd64 \
  --env "WK_NATIVE_PACKAGE_APT_MIRROR=${WK_NATIVE_PACKAGE_APT_MIRROR:-}" \
  --volume "$repository_root:/workspace:ro" \
  --volume "$package_dir:/packages:ro" \
  --volume "$work_dir:/work" \
  ubuntu:24.04 \
  bash -lc '
    set -euo pipefail
    if [[ -n "$WK_NATIVE_PACKAGE_APT_MIRROR" ]]; then
      sed -i \
        -e "s#http://archive.ubuntu.com/ubuntu/#$WK_NATIVE_PACKAGE_APT_MIRROR/#" \
        -e "s#http://security.ubuntu.com/ubuntu/#$WK_NATIVE_PACKAGE_APT_MIRROR/#" \
        /etc/apt/sources.list.d/ubuntu.sources
    fi
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install --yes --no-install-recommends \
      apt-utils createrepo-c dpkg-dev gnupg rpm python3 xz-utils zstd

    /workspace/scripts/generate-native-package-test-keyring.sh --output /work/keyring
    /workspace/scripts/build-native-package-repositories.sh \
      --packages-dir /packages \
      --output /work/unsigned \
      --apt-suite preview \
      --apt-architecture amd64 \
      --rpm-channel preview \
      --rpm-basearch x86_64
    source /work/keyring/manifest.env
    /workspace/scripts/sign-native-package-repositories.sh \
      --input /work/unsigned \
      --output /work/signed \
      --gnupg-home /work/keyring/gnupg \
      --test-only \
      --minimum-valid-days 0 \
      --apt-release apt/dists/preview/Release \
      --apt-primary-fingerprint "$WK_APT_PRIMARY_FINGERPRINT" \
      --apt-signing-fingerprint "$WK_APT_SIGNING_FINGERPRINT" \
      --apt-passphrase-file /work/keyring/apt-passphrase.txt \
      --rpm-repository rpm/preview/el/9/x86_64 \
      --rpm-primary-fingerprint "$WK_RPM_PRIMARY_FINGERPRINT" \
      --rpm-signing-fingerprint "$WK_RPM_SIGNING_FINGERPRINT" \
      --rpm-passphrase-file /work/keyring/rpm-passphrase.txt

    verify_test_repository() {
      /workspace/scripts/verify-native-package-repositories.sh \
        --repository "$1" \
        --allow-test-only \
        --minimum-valid-days 0 \
        --apt-release apt/dists/preview/Release \
        --apt-public-key "${2:-/work/keyring/public/apt.asc}" \
        --apt-primary-fingerprint "$WK_APT_PRIMARY_FINGERPRINT" \
        --apt-signing-fingerprint "$WK_APT_SIGNING_FINGERPRINT" \
        --rpm-repository rpm/preview/el/9/x86_64 \
        --rpm-public-key /work/keyring/public/rpm.asc \
        --rpm-primary-fingerprint "$WK_RPM_PRIMARY_FINGERPRINT" \
        --rpm-signing-fingerprint "$WK_RPM_SIGNING_FINGERPRINT"
    }

    verify_test_repository /work/signed
    if /workspace/scripts/verify-native-package-repositories.sh \
      --repository /work/signed \
      --apt-release apt/dists/preview/Release \
      --apt-public-key /work/keyring/public/apt.asc \
      --apt-primary-fingerprint "$WK_APT_PRIMARY_FINGERPRINT" \
      --apt-signing-fingerprint "$WK_APT_SIGNING_FINGERPRINT" \
      --rpm-repository rpm/preview/el/9/x86_64 \
      --rpm-public-key /work/keyring/public/rpm.asc \
      --rpm-primary-fingerprint "$WK_RPM_PRIMARY_FINGERPRINT" \
      --rpm-signing-fingerprint "$WK_RPM_SIGNING_FINGERPRINT" \
      >/tmp/default-verifier.out 2>&1; then
      echo "production verifier accepted test-only keys" >&2
      exit 1
    fi

    cp -a /work/signed /work/unsupported-apt-index
    apt_release=/work/unsupported-apt-index/apt/dists/preview/Release
    apt_suite_dir="${apt_release%/*}"
    apt_packages="$apt_suite_dir/main/binary-amd64/Packages"
    sed "s#Filename: pool/#Filename: pool/missing/#" "$apt_packages" \
      | xz --compress --stdout >"$apt_packages.xz"
    apt_xz_relative=main/binary-amd64/Packages.xz
    apt_xz_digest="$(sha256sum "$apt_packages.xz" | awk "{print \$1}")"
    apt_xz_size="$(stat -c %s "$apt_packages.xz")"
    awk -v digest="$apt_xz_digest" -v size="$apt_xz_size" -v relative="$apt_xz_relative" "
      \$0 == \"SHA256:\" && !inserted {
        print
        printf \" %s %16d %s\\n\", digest, size, relative
        inserted = 1
        next
      }
      { print }
      END { if (!inserted) exit 1 }
    " "$apt_release" >"$apt_release.tmp"
    mv "$apt_release.tmp" "$apt_release"
    cp "$apt_packages.xz" \
      "$apt_suite_dir/main/binary-amd64/by-hash/SHA256/$apt_xz_digest"
    rm -f "$apt_suite_dir/InRelease" "$apt_release.gpg"
    gpg --homedir /work/keyring/gnupg --batch --yes --pinentry-mode loopback \
      --passphrase-file /work/keyring/apt-passphrase.txt \
      --local-user "$WK_APT_SIGNING_FINGERPRINT!" --digest-algo SHA256 \
      --output "$apt_suite_dir/InRelease" --clearsign "$apt_release"
    gpg --homedir /work/keyring/gnupg --batch --yes --pinentry-mode loopback \
      --passphrase-file /work/keyring/apt-passphrase.txt \
      --local-user "$WK_APT_SIGNING_FINGERPRINT!" --digest-algo SHA256 --armor \
      --output "$apt_release.gpg" --detach-sign "$apt_release"
    if verify_test_repository /work/unsupported-apt-index \
      >/tmp/unsupported-apt-index.out 2>&1; then
      echo "signed unsupported APT Packages.xz was accepted" >&2
      exit 1
    fi
    grep -Fq "APT Release authenticates unsupported Packages indexes" \
      /tmp/unsupported-apt-index.out || {
        cat /tmp/unsupported-apt-index.out >&2
        echo "signed unsupported APT Packages.xz failed at the wrong validation layer" >&2
        exit 1
      }
    find /work/unsupported-apt-index -depth -delete

    cp -a /work/unsigned /work/unsigned-apt-index
    unsigned_packages=/work/unsigned-apt-index/apt/dists/preview/main/binary-amd64/Packages
    xz --compress --stdout "$unsigned_packages" >"$unsigned_packages.xz"
    if /workspace/scripts/sign-native-package-repositories.sh \
      --input /work/unsigned-apt-index \
      --output /work/unsupported-signed-output \
      --gnupg-home /work/keyring/gnupg \
      --test-only \
      --minimum-valid-days 0 \
      --apt-release apt/dists/preview/Release \
      --apt-primary-fingerprint "$WK_APT_PRIMARY_FINGERPRINT" \
      --apt-signing-fingerprint "$WK_APT_SIGNING_FINGERPRINT" \
      --apt-passphrase-file /work/keyring/apt-passphrase.txt \
      --rpm-repository rpm/preview/el/9/x86_64 \
      --rpm-primary-fingerprint "$WK_RPM_PRIMARY_FINGERPRINT" \
      --rpm-signing-fingerprint "$WK_RPM_SIGNING_FINGERPRINT" \
      --rpm-passphrase-file /work/keyring/rpm-passphrase.txt \
      >/tmp/unsupported-signer.out 2>&1; then
      echo "signer accepted an unsupported APT Packages.xz" >&2
      exit 1
    fi
    grep -Fq "APT repository contains an unsupported Packages index" \
      /tmp/unsupported-signer.out || {
        cat /tmp/unsupported-signer.out >&2
        echo "unsupported APT Packages.xz failed at the wrong signing layer" >&2
        exit 1
      }
    find /work/unsigned-apt-index -depth -delete

    if /workspace/scripts/sign-native-package-repositories.sh \
      --input /work/unsigned \
      --output /work/unsigned/nested-output \
      --gnupg-home /work/keyring/gnupg \
      --test-only \
      --minimum-valid-days 0 \
      --apt-release apt/dists/preview/Release \
      --apt-primary-fingerprint "$WK_APT_PRIMARY_FINGERPRINT" \
      --apt-signing-fingerprint "$WK_APT_SIGNING_FINGERPRINT" \
      --apt-passphrase-file /work/keyring/apt-passphrase.txt \
      --rpm-repository rpm/preview/el/9/x86_64 \
      --rpm-primary-fingerprint "$WK_RPM_PRIMARY_FINGERPRINT" \
      --rpm-signing-fingerprint "$WK_RPM_SIGNING_FINGERPRINT" \
      --rpm-passphrase-file /work/keyring/rpm-passphrase.txt \
      >/tmp/nested-output.out 2>&1; then
      echo "signer accepted an output repository inside its input" >&2
      exit 1
    fi
    grep -Fq "output repository must not be located inside the input repository" \
      /tmp/nested-output.out || {
        cat /tmp/nested-output.out >&2
        echo "nested signer output failed at the wrong validation layer" >&2
        exit 1
      }
    test ! -e /work/unsigned/nested-output

    cp -a /work/signed /work/tamper-deb
    find /work/tamper-deb/apt/pool -type f -name "*.deb" -delete
    if verify_test_repository /work/tamper-deb >/tmp/tamper-deb.out 2>&1; then
      echo "missing deb was accepted" >&2
      exit 1
    fi
    find /work/tamper-deb -depth -delete

    cp -a /work/signed /work/tamper-repodata
    cp /work/tamper-repodata/rpm/preview/el/9/x86_64/repodata/other.xml.gz \
      /work/tamper-repodata/rpm/preview/el/9/x86_64/repodata/primary.xml.gz
    if verify_test_repository /work/tamper-repodata >/tmp/tamper-repodata.out 2>&1; then
      echo "tampered RPM metadata was accepted" >&2
      exit 1
    fi
    find /work/tamper-repodata -depth -delete

    cp -a /work/signed /work/extra-rpm
    rpm_payload="$(find /work/extra-rpm/rpm/preview/el/9/x86_64/Packages -type f -name "*.rpm" -print -quit)"
    cp "$rpm_payload" "${rpm_payload%.rpm}.extra.rpm"
    if verify_test_repository /work/extra-rpm >/tmp/extra-rpm.out 2>&1; then
      echo "unindexed validly signed RPM was accepted" >&2
      exit 1
    fi
    find /work/extra-rpm -depth -delete

    gpg --homedir /work/keyring/gnupg --batch --armor \
      --output /work/expanded-apt-trust.asc \
      --export "$WK_APT_PRIMARY_FINGERPRINT" "$WK_RPM_PRIMARY_FINGERPRINT"
    if verify_test_repository /work/signed /work/expanded-apt-trust.asc \
      >/tmp/expanded-trust.out 2>&1; then
      echo "expanded APT key bundle was accepted" >&2
      exit 1
    fi

    cp /work/keyring/apt-passphrase.txt /work/insecure-passphrase.txt
    chmod 0644 /work/insecure-passphrase.txt
    if /workspace/scripts/sign-native-package-repositories.sh \
      --input /work/unsigned \
      --output /work/insecure-output \
      --gnupg-home /work/keyring/gnupg \
      --test-only \
      --minimum-valid-days 0 \
      --apt-release apt/dists/preview/Release \
      --apt-primary-fingerprint "$WK_APT_PRIMARY_FINGERPRINT" \
      --apt-signing-fingerprint "$WK_APT_SIGNING_FINGERPRINT" \
      --apt-passphrase-file /work/insecure-passphrase.txt \
      --rpm-repository rpm/preview/el/9/x86_64 \
      --rpm-primary-fingerprint "$WK_RPM_PRIMARY_FINGERPRINT" \
      --rpm-signing-fingerprint "$WK_RPM_SIGNING_FINGERPRINT" \
      --rpm-passphrase-file /work/keyring/rpm-passphrase.txt \
      >/tmp/insecure-passphrase.out 2>&1; then
      echo "group-readable signing passphrase was accepted" >&2
      exit 1
    fi

    cp -a /work/unsigned /work/special-input
    mkfifo /work/special-input/untrusted.fifo
    if /workspace/scripts/sign-native-package-repositories.sh \
      --input /work/special-input \
      --output /work/special-output \
      --gnupg-home /work/keyring/gnupg \
      --test-only \
      --minimum-valid-days 0 \
      --apt-release apt/dists/preview/Release \
      --apt-primary-fingerprint "$WK_APT_PRIMARY_FINGERPRINT" \
      --apt-signing-fingerprint "$WK_APT_SIGNING_FINGERPRINT" \
      --apt-passphrase-file /work/keyring/apt-passphrase.txt \
      --rpm-repository rpm/preview/el/9/x86_64 \
      --rpm-primary-fingerprint "$WK_RPM_PRIMARY_FINGERPRINT" \
      --rpm-signing-fingerprint "$WK_RPM_SIGNING_FINGERPRINT" \
      --rpm-passphrase-file /work/keyring/rpm-passphrase.txt \
      >/tmp/special-input.out 2>&1; then
      echo "special repository input was accepted" >&2
      exit 1
    fi
    find /work/special-input -depth -delete

    gpg --batch --dearmor \
      --output /work/apt-signing-key.gpg \
      /work/signed/keys/apt-signing-key.asc
    printf "%s\n" \
      "deb [arch=amd64 signed-by=/work/apt-signing-key.gpg] file:/work/signed/apt preview main" \
      >/tmp/wukongim-preview.list
    mkdir -p /tmp/apt-download
    cd /tmp/apt-download
    apt-get \
      -o Dir::Etc::sourcelist=/tmp/wukongim-preview.list \
      -o Dir::Etc::sourceparts=- \
      -o APT::Get::List-Cleanup=0 \
      update
    apt-get \
      -o Dir::Etc::sourcelist=/tmp/wukongim-preview.list \
      -o Dir::Etc::sourceparts=- \
      download wukongim
    test -f wukongim_*.deb
  '

docker run --rm \
  --platform linux/amd64 \
  --volume "$work_dir:/work:ro" \
  rockylinux:9 \
  bash -lc '
    set -euo pipefail
    cat >/etc/yum.repos.d/wukongim-preview.repo <<EOF
[wukongim-preview]
name=WuKongIM preview integration test
baseurl=file:///work/signed/rpm/preview/el/9/x86_64
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=file:///work/signed/keys/rpm-signing-key.asc
EOF
    # Keep the dependency source explicit: wukongim correctly requires
    # systemd, which is provided by BaseOS rather than the repository under
    # test. The preview repository still enforces both metadata and package
    # signature verification through repo_gpgcheck/gpgcheck above.
    dnf --disablerepo="*" \
      --enablerepo=baseos \
      --enablerepo=appstream \
      --enablerepo=wukongim-preview \
      --setopt=install_weak_deps=False install --assumeyes wukongim
    version_output="$(/usr/bin/wukongim version --output json)"
    grep -Fq "\"build_source\":\"release\"" <<<"$version_output"
    test ! -e /etc/wukongim/wukongim.toml
  '

echo "native-package signed repository container validation passed"
