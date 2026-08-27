import type { Metadata } from 'next';
import type { Locale } from './navigation';

export const appName = 'WuKongIM Docs';
export const defaultSiteUrl = 'https://docs.githubim.com';

/** Resolves the public documentation origin from a build-only environment value. */
export function resolveSiteUrl(value = process.env.DOCS_SITE_URL): string {
  if (!value) return defaultSiteUrl;

  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new Error('DOCS_SITE_URL must be an absolute HTTPS origin');
  }

  if (url.protocol !== 'https:') {
    throw new Error('DOCS_SITE_URL must be an absolute HTTPS origin');
  }
  if (url.username || url.password) {
    throw new Error('DOCS_SITE_URL must not contain credentials');
  }
  if ((url.pathname && url.pathname !== '/') || url.search || url.hash) {
    throw new Error('DOCS_SITE_URL must contain only an origin');
  }

  return url.origin;
}

export const siteUrl = resolveSiteUrl();
export const docsContentRoute = '/llms.mdx';

export const gitConfig = {
  user: 'WuKongIM',
  repo: 'WuKongIM',
  branch: 'main',
};

/** Returns an absolute page URL under the configured canonical origin. */
export function canonicalUrl(path: string, origin = siteUrl): string {
  const normalizedPath = `/${path}`.replaceAll(/\/{2,}/g, '/').replace(/\/?$/, '/');
  return new URL(normalizedPath, `${resolveSiteUrl(origin)}/`).toString();
}

type PreviewEnvironment = Partial<Pick<NodeJS.ProcessEnv, 'DOCS_PREVIEW' | 'VERCEL_ENV'>>;

/** Detects an explicitly marked preview build without relying on a public runtime variable. */
export function isPreviewBuild(
  environment: PreviewEnvironment = process.env as PreviewEnvironment,
): boolean {
  const explicit = environment.DOCS_PREVIEW?.trim().toLowerCase();
  if (explicit === 'true' || explicit === '1') return true;
  if (explicit === 'false' || explicit === '0') return false;
  return environment.VERCEL_ENV === 'preview';
}

/** Keeps planned and preview pages out of indexes while allowing production publication. */
export function getRobotsMetadata(
  published: boolean,
  preview = isPreviewBuild(),
): Metadata['robots'] {
  if (preview) {
    return {
      index: false,
      follow: false,
      googleBot: { index: false, follow: false },
    };
  }
  if (published) return { index: true, follow: true };

  return {
    index: false,
    follow: true,
    googleBot: { index: false, follow: true },
  };
}

interface DocumentationFeedbackOptions {
  locale: Locale;
  pageTitle: string;
  pagePath: string;
  siteOrigin?: string;
}

/** Builds a prefilled, non-tracking GitHub issue link for one published page. */
export function getDocumentationFeedbackUrl({
  locale,
  pageTitle,
  pagePath,
  siteOrigin = siteUrl,
}: DocumentationFeedbackOptions): string {
  const origin = resolveSiteUrl(siteOrigin);
  const pageUrl = canonicalUrl(pagePath, origin);
  const compatibilityUrl = new URL('/compatibility.json', `${origin}/`).toString();
  const issue = new URL(
    `https://github.com/${gitConfig.user}/${gitConfig.repo}/issues/new`,
  );

  issue.searchParams.set('title', `[Docs] ${pageTitle}`);
  issue.searchParams.set(
    'body',
    [
      '## Documentation context',
      '',
      `- Page: ${pageUrl}`,
      `- Language: ${locale}`,
      `- Compatibility snapshot: ${compatibilityUrl}`,
      '',
      '## What needs improvement?',
      '',
      '<!-- Describe what was unclear, incorrect, or missing. -->',
    ].join('\n'),
  );

  return issue.toString();
}
