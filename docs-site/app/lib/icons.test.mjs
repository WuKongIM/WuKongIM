import test from 'node:test';
import assert from 'node:assert/strict';
import { isValidElement } from 'react';
import { resolveDocsIcon } from './icons.ts';

test('resolveDocsIcon returns a React element for configured docs icons', () => {
  const icon = resolveDocsIcon('server');

  assert.ok(isValidElement(icon));
});

test('resolveDocsIcon returns undefined for unknown docs icons', () => {
  assert.equal(resolveDocsIcon('unknown-icon'), undefined);
  assert.equal(resolveDocsIcon(undefined), undefined);
});
