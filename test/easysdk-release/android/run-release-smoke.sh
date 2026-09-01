#!/usr/bin/env bash

set -euo pipefail

./gradlew :example:connectedDebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArguments.class=com.githubim.easysdk.example.ReleaseSmokeTest \
  -Pandroid.testInstrumentationRunnerArguments.aliceUid="${ALICE_UID}" \
  -Pandroid.testInstrumentationRunnerArguments.aliceToken="${ALICE_TOKEN}" \
  -Pandroid.testInstrumentationRunnerArguments.bobUid="${BOB_UID}" \
  -Pandroid.testInstrumentationRunnerArguments.aliceToBobText="${ALICE_TO_BOB_TEXT}" \
  -Pandroid.testInstrumentationRunnerArguments.bobToAliceText="${BOB_TO_ALICE_TEXT}"
