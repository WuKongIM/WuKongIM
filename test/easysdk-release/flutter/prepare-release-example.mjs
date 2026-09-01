import fs from 'node:fs';
import path from 'node:path';

const sdkRoot = process.argv[2];
if (!sdkRoot) {
  throw new Error('Flutter SDK checkout path is required');
}

const pubspec = path.join(sdkRoot, 'example', 'pubspec.yaml');
let source = fs.readFileSync(pubspec, 'utf8');

function replaceExactly(oldValue, newValue) {
  if (!source.includes(oldValue)) {
    throw new Error(`Expected Flutter example fragment was not found: ${oldValue}`);
  }
  source = source.replace(oldValue, newValue);
}

replaceExactly(
  '  wukong_easy_sdk:\n    path: ../',
  '  wukong_easy_sdk: 1.1.0',
);

fs.writeFileSync(pubspec, source);
