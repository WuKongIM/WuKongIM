import { describe, expect, test } from 'bun:test';

async function read(relativePath: string) {
  return Bun.file(new URL(relativePath, import.meta.url)).text();
}

describe('Gateway token-authentication documentation', () => {
  test('keeps the secure default prominent in configuration pages', async () => {
    const pages = await Promise.all([
      read('../content/docs/server/configuration/common-configurations.mdx'),
      read('../content/docs/server/configuration/common-configurations.en.mdx'),
      read('../content/docs/server/configuration/security.mdx'),
      read('../content/docs/server/configuration/security.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain('gateway.token_auth_on');
      expect(page).toContain('WK_GATEWAY_TOKEN_AUTH_ON');
      expect(page).toContain('ReasonAuthFail');
      expect(page).toContain('true');
    }
  });

  test('publishes the exact-match behavior across configuration and protocol guidance', async () => {
    const pages = await Promise.all([
      read('../content/docs/server/configuration/index.mdx'),
      read('../content/docs/server/configuration/index.en.mdx'),
      read('../content/docs/api/dictionaries/device-flags.mdx'),
      read('../content/docs/api/dictionaries/device-flags.en.mdx'),
      read('../content/docs/api/client-protocols/json-rpc.mdx'),
      read('../content/docs/api/client-protocols/json-rpc.en.mdx'),
    ]);

    for (const page of pages) {
      expect(page).toContain('gateway.token_auth_on');
      expect(page).toContain('true');
    }
    for (const page of pages.slice(2, 4)) expect(page).toContain('ReasonAuthFail');
  });
});
