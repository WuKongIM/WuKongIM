#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <flutter-sdk-checkout>" >&2
  exit 2
fi

readonly SDK_ROOT="$1"
readonly PUBSPEC="${SDK_ROOT}/example/pubspec.yaml"
readonly INFO_PLIST="${SDK_ROOT}/example/ios/Runner/Info.plist"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

node "${SCRIPT_DIR}/prepare-release-example.mjs" "${SDK_ROOT}"

/usr/libexec/PlistBuddy -c 'Add :NSAppTransportSecurity dict' "${INFO_PLIST}"
/usr/libexec/PlistBuddy -c 'Add :NSAppTransportSecurity:NSAllowsArbitraryLoads bool true' "${INFO_PLIST}"
/usr/libexec/PlistBuddy -c 'Add :NSLocalNetworkUsageDescription string Connect to the local WuKongIM release acceptance server.' "${INFO_PLIST}"

grep -Fq '  wukong_easy_sdk: 1.1.0' "${PUBSPEC}"
grep -Fq '  integration_test:' "${PUBSPEC}"
/usr/libexec/PlistBuddy -c 'Print :NSAppTransportSecurity:NSAllowsArbitraryLoads' "${INFO_PLIST}" \
  | grep -Fxq true
