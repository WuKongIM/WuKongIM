import { describe, expect, test } from 'bun:test';
import {
  currentImageTagToken,
  currentReleaseTagToken,
  remarkReleaseVersion,
  resolveReleaseVersion,
} from './release-version';

describe('documentation release version', () => {
  test('resolves the first exact release heading and ignores Unreleased', () => {
    const release = resolveReleaseVersion(`
## [Unreleased]

## [v4.2.0-rc.3] - 2026-09-04

## [v4.1.0] - 2026-08-01
`);

    expect(release).toEqual({
      tag: 'v4.2.0-rc.3',
      imageTag: '4.2.0-rc.3',
      prerelease: true,
    });
  });

  test('requires an expected release tag to match the latest heading', () => {
    const changelog = '## [v4.2.0] - 2026-09-04\n';
    expect(resolveReleaseVersion(changelog, 'v4.2.0').tag).toBe('v4.2.0');
    expect(() => resolveReleaseVersion(changelog, 'v4.2.1')).toThrow(
      'latest Changelog release v4.2.0 does not match release v4.2.1',
    );
  });

  test('rejects missing, duplicate, and malformed release headings', () => {
    expect(() => resolveReleaseVersion('## [Unreleased]\n')).toThrow(
      'does not contain an exact release heading',
    );
    expect(() =>
      resolveReleaseVersion(
        '## [v4.2.0] - 2026-09-04\n## [v4.2.0] - 2026-09-03\n',
      ),
    ).toThrow('duplicate release heading');
    expect(() => resolveReleaseVersion('## [v4.02.0] - 2026-09-04\n')).toThrow(
      'must be strict SemVer',
    );
    expect(() => resolveReleaseVersion('## [v4.2.0-rc.03] - 2026-09-04\n')).toThrow(
      'must not have leading zeroes',
    );
  });

  test('replaces release and image tokens throughout a Markdown tree', () => {
    const tree = {
      type: 'root',
      children: [
        { type: 'text', value: `release ${currentReleaseTagToken}` },
        {
          type: 'code',
          value: `image: ghcr.io/wukongim/wukongim:${currentImageTagToken}`,
        },
      ],
    };

    remarkReleaseVersion(resolveReleaseVersion('## [v4.2.0-rc.3] - 2026-09-04\n'))(tree);

    expect(tree.children[0].value).toBe('release v4.2.0-rc.3');
    expect(tree.children[1].value).toBe(
      'image: ghcr.io/wukongim/wukongim:4.2.0-rc.3',
    );
  });

  test('accepts the repository Changelog as the production source', async () => {
    const changelog = await Bun.file(new URL('../../CHANGELOG.md', import.meta.url)).text();
    const release = resolveReleaseVersion(changelog);

    expect(release.imageTag).toBe(release.tag.slice(1));
  });
});
