import { describe, expect, test } from 'bun:test';

const contentRoot = new URL('../content/docs/guide/core-concepts/', import.meta.url);
const conceptSlugs = ['messages', 'channels', 'users', 'devices', 'conversations'] as const;

async function content(fileName: string) {
  return Bun.file(new URL(fileName, contentRoot)).text();
}

describe('core concept content contract', () => {
  test('keeps the overview focused on five application concepts', async () => {
    const [zh, en] = await Promise.all([content('index.mdx'), content('index.en.mdx')]);

    for (const slug of conceptSlugs) {
      expect(zh).toContain(`/zh/guide/core-concepts/${slug}`);
      expect(en).toContain(`/en/guide/core-concepts/${slug}`);
    }

    expect(zh).toContain('/zh/server/architecture');
    expect(en).toContain('/en/server/architecture');
    expect(zh).not.toContain('/zh/guide/core-concepts/cluster-and-nodes');
    expect(en).not.toContain('/en/guide/core-concepts/users-and-devices');
  });

  test('introduces each concept through purpose and relationships before architecture details', async () => {
    for (const slug of conceptSlugs) {
      const [zh, en] = await Promise.all([content(`${slug}.mdx`), content(`${slug}.en.mdx`)]);

      expect(zh).toContain('## 为什么');
      expect(zh).toContain('## 与其他概念的关系');
      expect(en).toMatch(/^## Why /mu);
      expect(en).toContain('## Relationship to other concepts');
      expect(zh.match(/^## .+$/gmu)?.slice(0, 2)).toEqual([
        expect.stringMatching(/^## 为什么/u),
        '## 与其他概念的关系',
      ]);
      expect(en.match(/^## .+$/gmu)?.slice(0, 2)).toEqual([
        expect.stringMatching(/^## Why /u),
        '## Relationship to other concepts',
      ]);
      expect(zh).not.toMatch(/Presence Authority|Slot Leader|Channel Leader|write fence|owner-push|tombstone|quorum/u);
      expect(en).not.toMatch(/Presence Authority|Slot Leader|Channel Leader|write fence|owner-push|tombstone|quorum/u);
      expect(zh).not.toBe(en);
    }
  });
});
