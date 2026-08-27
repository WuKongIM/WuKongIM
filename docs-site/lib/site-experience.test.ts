import { describe, expect, test } from 'bun:test';
import { renderToStaticMarkup } from 'react-dom/server';
import HomePage from '../app/[lang]/(home)/page';
import {
  findBrokenInternalLinks,
  getBasicAccessibilityIssues,
  type StaticHtmlPage,
} from '../scripts/check-static-output';
import {
  canonicalUrl,
  defaultSiteUrl,
  getDocumentationFeedbackUrl,
  getRobotsMetadata,
  isPreviewBuild,
  resolveSiteUrl,
} from './shared';
import { getDomainPublicationCounts, getPublishedFooterItems } from './navigation-tree';

describe('documentation site experience', () => {
  test('uses a safe configurable canonical origin and normalized trailing slashes', () => {
    expect(resolveSiteUrl('')).toBe(defaultSiteUrl);
    expect(resolveSiteUrl('https://preview.docs.example.com/')).toBe(
      'https://preview.docs.example.com',
    );
    expect(() => resolveSiteUrl('http://docs.example.com')).toThrow('HTTPS origin');
    expect(() => resolveSiteUrl('https://user:secret@docs.example.com')).toThrow(
      'credentials',
    );
    expect(() => resolveSiteUrl('https://docs.example.com/subpath')).toThrow('origin');
    expect(canonicalUrl('/en/sdk/javascript/quickstart', 'https://docs.example.com')).toBe(
      'https://docs.example.com/en/sdk/javascript/quickstart/',
    );
  });

  test('marks preview pages noindex without weakening published production pages', () => {
    expect(isPreviewBuild({ DOCS_PREVIEW: 'true' })).toBe(true);
    expect(isPreviewBuild({ VERCEL_ENV: 'preview' })).toBe(true);
    expect(isPreviewBuild({ DOCS_PREVIEW: 'false', VERCEL_ENV: 'production' })).toBe(false);
    expect(getRobotsMetadata(true, false)).toEqual({ index: true, follow: true });
    expect(getRobotsMetadata(true, true)).toEqual({
      index: false,
      follow: false,
      googleBot: { index: false, follow: false },
    });
  });

  test('derives honest publication counts for every home-page domain', () => {
    for (const domain of ['guide', 'server', 'sdk', 'api'] as const) {
      const counts = getDomainPublicationCounts('en', domain);
      expect(counts.published + counts.planned).toBe(counts.total);
      expect(counts.total).toBeGreaterThan(0);
    }
  });

  test('renders an application-developer entry to the JavaScript golden path', async () => {
    const html = renderToStaticMarkup(
      await HomePage({ params: Promise.resolve({ lang: 'en' }) }),
    );

    expect(html).toContain('href="/en/sdk/javascript/quickstart"');
    expect(html).not.toContain('phase-one');
    expect(html).not.toContain('Navigation ready · detailed content planned');

    for (const domain of ['guide', 'server', 'sdk', 'api'] as const) {
      const counts = getDomainPublicationCounts('en', domain);
      expect(html).toContain(`${counts.published} published`);
      expect(html).toContain(`${counts.planned} planned`);
    }
  });

  test('skips planned routes in previous and next page navigation', () => {
    expect(getPublishedFooterItems('en', 'server', '/en/server/deployment/linux')).toEqual({
      previous: expect.objectContaining({ url: '/en/server/deployment/docker' }),
      next: expect.objectContaining({ url: '/en/server/deployment/multi-node' }),
    });
  });

  test('prefills a localized GitHub documentation issue with page context', () => {
    const issueUrl = new URL(
      getDocumentationFeedbackUrl({
        locale: 'en',
        pageTitle: 'Quickstart',
        pagePath: '/en/sdk/javascript/quickstart',
        siteOrigin: defaultSiteUrl,
      }),
    );

    expect(issueUrl.origin + issueUrl.pathname).toBe(
      'https://github.com/WuKongIM/WuKongIM/issues/new',
    );
    expect(issueUrl.searchParams.get('title')).toContain('Quickstart');
    expect(issueUrl.searchParams.get('body')).toContain(
      'https://docs.githubim.com/en/sdk/javascript/quickstart/',
    );
    expect(issueUrl.searchParams.get('body')).toContain('Language: en');
    expect(issueUrl.searchParams.get('body')).toContain(
      'https://docs.githubim.com/compatibility.json',
    );
  });

  test('reports broken internal routes while ignoring external links and fragments', () => {
    const pages: StaticHtmlPage[] = [
      {
        filePath: 'en/index.html',
        html: [
          '<html lang="en"><main><h1>Docs</h1>',
          '<a href="/en/guide/">Guide</a>',
          '<a href="/en/missing">Missing</a>',
          '<a href="#intro">Intro</a>',
          '<a href="https://github.com/WuKongIM/WuKongIM">GitHub</a>',
          '</main></html>',
        ].join(''),
      },
      {
        filePath: 'en/guide/index.html',
        html: '<html lang="en"><main><h1>Guide</h1></main></html>',
      },
    ];

    expect(
      findBrokenInternalLinks(pages, new Set(pages.map((page) => page.filePath))),
    ).toEqual([
      {
        from: 'en/index.html',
        href: '/en/missing',
        resolvedPath: '/en/missing',
      },
    ]);
  });

  test('checks basic page landmarks, heading structure, language, and image text alternatives', () => {
    expect(
      getBasicAccessibilityIssues(
        '<html lang="en"><body><main><h1>Quickstart</h1><img src="/logo.png" alt="" /></main></body></html>',
        'en',
      ),
    ).toEqual([]);

    expect(
      getBasicAccessibilityIssues(
        '<html lang="zh"><body><main><img src="/diagram.png" /></main></body></html>',
        'zh',
      ),
    ).toEqual(expect.arrayContaining(['expected exactly one h1', 'image is missing alt text']));
  });
});
