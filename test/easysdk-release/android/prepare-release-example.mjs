import fs from 'node:fs';
import path from 'node:path';

const sdkRoot = process.argv[2];
if (!sdkRoot) {
  throw new Error('Android SDK checkout path is required');
}

const buildFile = path.join(sdkRoot, 'example', 'build.gradle');
let source = fs.readFileSync(buildFile, 'utf8');

function replaceExactly(oldValue, newValue) {
  if (!source.includes(oldValue)) {
    throw new Error(`Expected Android example fragment was not found: ${oldValue}`);
  }
  source = source.replace(oldValue, newValue);
}

replaceExactly(
  "    // Use the local SDK module\n    implementation project(':')",
  "    // Acceptance must resolve the immutable Maven Central release.\n" +
    "    implementation 'com.githubim:easysdk-android:1.0.5'",
);
replaceExactly(
  "    androidTestImplementation 'androidx.test.espresso:espresso-core:3.5.1'",
  "    androidTestImplementation 'androidx.test.espresso:espresso-core:3.5.1'\n" +
    "    androidTestImplementation 'org.jetbrains.kotlinx:kotlinx-coroutines-android:1.7.3'",
);

fs.writeFileSync(buildFile, source);
