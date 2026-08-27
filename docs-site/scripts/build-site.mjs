import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { closeSync, fstatSync, openSync, readFileSync, readSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const docsRoot = fileURLToPath(new URL('..', import.meta.url));
const environment = { ...process.env };
const receiptPath = environment.WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH;
const suppliedReceiptJson = environment.WK_DOCS_GOLDEN_PATH_RECEIPT_JSON;

if (receiptPath && suppliedReceiptJson) {
  throw new Error(
    'WK_DOCS_GOLDEN_PATH_ATTESTATION_PATH and WK_DOCS_GOLDEN_PATH_RECEIPT_JSON cannot both be set',
  );
}

// Only the bounded file input may supply receipt contents to the static build.
delete environment.WK_DOCS_GOLDEN_PATH_RECEIPT_JSON;
if (receiptPath) {
  try {
    const absoluteReceiptPath = resolve(docsRoot, receiptPath);
    const descriptor = openSync(absoluteReceiptPath, 'r');
    try {
      const receiptStat = fstatSync(descriptor);
      if (!receiptStat.isFile() || receiptStat.size === 0 || receiptStat.size > 16 * 1024) {
        throw new Error('receipt must be a non-empty regular file no larger than 16 KiB');
      }
      const receipt = Buffer.alloc(receiptStat.size + 1);
      const bytesRead = readSync(descriptor, receipt, 0, receipt.length, 0);
      if (bytesRead !== receiptStat.size) {
        throw new Error('receipt changed while it was being read');
      }
      environment.WK_DOCS_GOLDEN_PATH_RECEIPT_JSON = receipt.subarray(0, bytesRead).toString('utf8');
    } finally {
      closeSync(descriptor);
    }
  } catch {
    // Preserve a malformed marker without exposing the path or file contents.
    environment.WK_DOCS_GOLDEN_PATH_RECEIPT_JSON = '{';
    console.warn(
      'Golden-path verification receipt could not be loaded; publishing an unverified snapshot.',
    );
  }
}

if (!environment.WK_DOCS_SOURCE_REVISION) {
  const revision = spawnSync('git', ['rev-parse', '--verify', 'HEAD'], {
    cwd: docsRoot,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'ignore'],
  });
  if (revision.status === 0 && revision.stdout.trim() !== '') {
    environment.WK_DOCS_SOURCE_REVISION = revision.stdout.trim();
  }
}

const lockfile = readFileSync(
  new URL('../examples/javascript-web-quickstart/package-lock.json', import.meta.url),
);
environment.WK_DOCS_SAMPLE_LOCK_SHA256 = createHash('sha256').update(lockfile).digest('hex');

const nextBinary = fileURLToPath(
  new URL(`../node_modules/.bin/next${process.platform === 'win32' ? '.cmd' : ''}`, import.meta.url),
);
const result = spawnSync(nextBinary, ['build'], {
  cwd: docsRoot,
  env: environment,
  stdio: 'inherit',
});

if (result.error) {
  throw result.error;
}
process.exit(result.status ?? 1);
