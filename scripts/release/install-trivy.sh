#!/usr/bin/env bash
# Install the reviewed scanner bytes without the vendor redirect service.
set -euo pipefail

case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) ;;
  *) echo 'Trivy release bootstrap requires Linux amd64' >&2; exit 2 ;;
esac
: "${RUNNER_TEMP:?GitHub runner scratch directory is required}"
: "${GITHUB_PATH:?GitHub PATH publication file is required}"

trivy_version=0.74.0
trivy_archive="trivy_${trivy_version}_Linux-64bit.tar.gz"
trivy_checksums="trivy_${trivy_version}_checksums.txt"
trivy_url="https://github.com/aquasecurity/trivy/releases/download/v${trivy_version}"
trivy_scratch="$(mktemp -d "$RUNNER_TEMP/trivy-install.XXXXXXXX")"
trap 'rm -rf -- "$trivy_scratch"' EXIT
trivy_bin_dir="$RUNNER_TEMP/trivy-bin"

for asset in "$trivy_archive" "$trivy_checksums"; do
  curl --fail --location --retry 3 --retry-max-time 240 \
    --connect-timeout 15 --max-time 120 \
    --proto '=https' --proto-redir '=https' \
    "$trivy_url/$asset" -o "$trivy_scratch/$asset"
done

(
  cd "$trivy_scratch"
  printf '%s  %s\n' \
    2ae6fe3ee734b7fdf11335663e18c75ea12dccc76062f09f164a3b0f8be4371a "$trivy_archive" \
    bc701c3c3ee8b9acbea2c23257e41381e3854888f51281616a6ba5dc96963821 "$trivy_checksums" \
    | sha256sum --check --strict
  tar -xzf "$trivy_archive" trivy
)
install -d -m 0755 "$trivy_bin_dir"
install -m 0755 "$trivy_scratch/trivy" "$trivy_bin_dir/trivy"
"$trivy_bin_dir/trivy" --version
printf '%s\n' "$trivy_bin_dir" >> "$GITHUB_PATH"
