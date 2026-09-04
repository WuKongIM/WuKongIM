import { describe, expect, test } from 'bun:test';
import { baseOptions } from './layout.shared';

describe('resource links', () => {
  test.each([
    ['zh', '资源', '官网'],
    ['en', 'Resources', 'Website'],
  ] as const)(
    'keeps the %s resource menu aligned with public endpoints',
    (locale, label, websiteLabel) => {
      const resourceMenu = baseOptions(locale).links?.find(
        (link) => link.type === 'menu' && link.text === label,
      );

      expect(resourceMenu).toBeDefined();
      expect(resourceMenu).not.toHaveProperty(
        'items',
        expect.arrayContaining([expect.objectContaining({ text: websiteLabel })]),
      );
      expect(resourceMenu).toHaveProperty(
        'items',
        expect.arrayContaining([
          expect.objectContaining({ url: 'https://demo.githubim.com/' }),
          expect.objectContaining({ url: 'https://manager.githubim.com/' }),
        ]),
      );
    },
  );
});
