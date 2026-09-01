#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <android-sdk-checkout>" >&2
  exit 2
fi

readonly SDK_ROOT="$1"
readonly BUILD_FILE="${SDK_ROOT}/example/build.gradle"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

node "${SCRIPT_DIR}/prepare-release-example.mjs" "${SDK_ROOT}"

grep -Fq "implementation 'com.githubim:easysdk-android:1.0.5'" "${BUILD_FILE}"
grep -Fq "androidTestImplementation 'org.jetbrains.kotlinx:kotlinx-coroutines-android:1.7.3'" "${BUILD_FILE}"
if grep -Fq "implementation project(':')" "${BUILD_FILE}"; then
  echo "Local Android SDK dependency remained after release preparation" >&2
  exit 1
fi
