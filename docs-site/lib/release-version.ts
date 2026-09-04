export const currentReleaseTagToken = 'WK_CURRENT_RELEASE_TAG';
export const currentImageTagToken = 'WK_CURRENT_IMAGE_TAG';

export type ReleaseVersion = Readonly<{
  tag: string;
  imageTag: string;
  prerelease: boolean;
}>;

type MarkdownNode = {
  value?: unknown;
  children?: MarkdownNode[];
};

const releaseHeadingPattern = /^## \[(v[^\]\r\n]+)\] - \d{4}-\d{2}-\d{2}$/gm;
const semverTagPattern =
  /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/;

function requireStrictSemverTag(tag: string): RegExpMatchArray {
  const match = tag.match(semverTagPattern);
  if (!match) {
    throw new Error(`release tag must be strict SemVer with a leading v: ${tag}`);
  }

  const prerelease = match[4];
  if (prerelease) {
    for (const identifier of prerelease.split('.')) {
      if (/^\d+$/.test(identifier) && identifier.length > 1 && identifier.startsWith('0')) {
        throw new Error(`numeric prerelease identifiers must not have leading zeroes: ${tag}`);
      }
    }
  }
  return match;
}

/** Resolve the current public version from the first exact release heading. */
export function resolveReleaseVersion(
  changelog: string,
  expectedTag?: string,
): ReleaseVersion {
  const tags = [...changelog.matchAll(releaseHeadingPattern)].map((match) => match[1]);
  if (tags.length === 0) {
    throw new Error('CHANGELOG.md does not contain an exact release heading');
  }

  const uniqueTags = new Set(tags);
  if (uniqueTags.size !== tags.length) {
    throw new Error('CHANGELOG.md contains a duplicate release heading');
  }

  for (const tag of tags) requireStrictSemverTag(tag);

  const tag = tags[0];
  if (expectedTag) {
    requireStrictSemverTag(expectedTag);
    if (tag !== expectedTag) {
      throw new Error(`latest Changelog release ${tag} does not match release ${expectedTag}`);
    }
  }

  return Object.freeze({
    tag,
    imageTag: tag.slice(1),
    prerelease: tag.includes('-'),
  });
}

function replaceReleaseTokens(node: MarkdownNode, release: ReleaseVersion): void {
  if (typeof node.value === 'string') {
    node.value = node.value
      .replaceAll(currentReleaseTagToken, release.tag)
      .replaceAll(currentImageTagToken, release.imageTag);
  }
  for (const child of node.children ?? []) replaceReleaseTokens(child, release);
}

/** Replace current-version tokens in every string-valued Markdown node. */
export function remarkReleaseVersion(release: ReleaseVersion) {
  return (tree: MarkdownNode) => replaceReleaseTokens(tree, release);
}
