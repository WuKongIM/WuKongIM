import { getMDXComponents } from '@/components/mdx';
import { gitConfig, siteUrl } from '@/lib/shared';
import {
  domains,
  getAllNavigationEntries,
  getNavigationEntry,
  locales,
  parseLocale,
  type DocumentationDomain,
} from '@/lib/navigation';
import { getPageMarkdownUrl, source } from '@/lib/source';
import {
  DocsBody,
  DocsDescription,
  DocsPage,
  DocsTitle,
  MarkdownCopyButton,
  ViewOptionsPopover,
} from 'fumadocs-ui/layouts/docs/page';
import { createRelativeLink } from 'fumadocs-ui/mdx';
import { Clock3 } from 'lucide-react';
import type { Metadata } from 'next';
import { notFound } from 'next/navigation';

interface DocumentationPageParams {
  lang: string;
  section: string;
  slug?: string[];
}

function parseParams(values: DocumentationPageParams) {
  const locale = parseLocale(values.lang);
  const domain = domains.find((candidate) => candidate.key === values.section) as
    | DocumentationDomain
    | undefined;
  if (!locale || !domain) return;

  const slugs = values.slug ?? [];
  const entry = getNavigationEntry(locale, domain.key, slugs);
  if (!entry) return;

  return { locale, domain, slugs, entry };
}

export default async function DocumentationPage({
  params,
}: {
  params: Promise<DocumentationPageParams>;
}) {
  const resolved = parseParams(await params);
  if (!resolved) notFound();

  const { domain, entry, locale, slugs } = resolved;
  const page = source.getPage([domain.key, ...slugs], locale);

  if (entry.status === 'planned') {
    return (
      <DocsPage toc={[]}>
        <div className="mb-5 inline-flex items-center gap-2 rounded-full border border-orange-200 bg-orange-50 px-3 py-1 text-xs font-semibold text-orange-700 dark:border-orange-900 dark:bg-orange-950 dark:text-orange-300">
          <Clock3 className="size-3.5" />
          {locale === 'zh' ? '规划中' : 'Planned'}
        </div>
        <DocsTitle>{entry.label}</DocsTitle>
        <DocsDescription>{entry.description}</DocsDescription>
        <DocsBody>
          <div className="mt-8 rounded-2xl border border-dashed border-fd-border bg-fd-muted/40 p-6">
            <h2>{locale === 'zh' ? '本页内容边界' : 'Scope of this page'}</h2>
            <p>{entry.description}</p>
            <p className="text-sm text-fd-muted-foreground">
              {locale === 'zh'
                ? '该页面已纳入 v3 文档信息架构。正文完成并通过中英文校验后，才会进入搜索、Sitemap 和 LLM 索引。'
                : 'This page is part of the v3 information architecture. It will enter search, sitemap, and LLM indexes only after both language versions are complete.'}
            </p>
          </div>
        </DocsBody>
      </DocsPage>
    );
  }

  if (!page) notFound();
  const MDX = page.data.body;
  const markdownUrl = getPageMarkdownUrl(page).url;

  return (
    <DocsPage toc={page.data.toc} full={page.data.full}>
      <DocsTitle>{page.data.title}</DocsTitle>
      <DocsDescription className="mb-0">{page.data.description}</DocsDescription>
      <div className="flex flex-row items-center gap-2 border-b pb-6">
        <MarkdownCopyButton markdownUrl={markdownUrl} />
        <ViewOptionsPopover
          markdownUrl={markdownUrl}
          githubUrl={`https://github.com/${gitConfig.user}/${gitConfig.repo}/blob/${gitConfig.branch}/docs-site/content/docs/${page.path}`}
        />
      </div>
      <DocsBody>
        <MDX
          components={getMDXComponents({
            a: createRelativeLink(source, page),
          })}
        />
      </DocsBody>
    </DocsPage>
  );
}

export function generateStaticParams() {
  return locales.flatMap((locale) =>
    getAllNavigationEntries(locale).map((entry) => ({
      lang: locale,
      section: entry.domain,
      slug: entry.slugs,
    })),
  );
}

export async function generateMetadata({
  params,
}: {
  params: Promise<DocumentationPageParams>;
}): Promise<Metadata> {
  const resolved = parseParams(await params);
  if (!resolved) notFound();
  const { entry, locale } = resolved;

  return {
    title: entry.label,
    description: entry.description,
    alternates: {
      canonical: entry.url,
      languages: {
        zh: entry.url.replace(`/${locale}/`, '/zh/'),
        en: entry.url.replace(`/${locale}/`, '/en/'),
      },
    },
    robots:
      entry.status === 'published'
        ? { index: true, follow: true }
        : { index: false, follow: true, googleBot: { index: false, follow: true } },
    openGraph:
      entry.status === 'published'
        ? {
            type: 'article',
            url: new URL(entry.url, siteUrl),
            title: entry.label,
            description: entry.description,
          }
        : undefined,
  };
}
