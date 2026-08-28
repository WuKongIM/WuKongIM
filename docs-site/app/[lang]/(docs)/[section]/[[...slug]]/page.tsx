import { getMDXComponents } from '@/components/mdx';
import { DocsMainContainer } from '@/components/docs-main-container';
import { OpenAPIPage } from '@/components/openapi-page';
import {
  canonicalUrl,
  getDocumentationFeedbackUrl,
  getRobotsMetadata,
  gitConfig,
} from '@/lib/shared';
import {
  domains,
  getAllNavigationEntries,
  getNavigationEntry,
  locales,
  parseLocale,
  type DocumentationDomain,
} from '@/lib/navigation';
import { getPageMarkdownUrl, source } from '@/lib/source';
import { openapi } from '@/lib/openapi';
import {
  productHTTPOpenAPIContractFiles,
  productHTTPOpenAPIReferenceGroups,
} from '@/lib/product-http-openapi';
import {
  serviceOpenAPIContractForSlugs,
  serviceOpenAPIContracts,
} from '@/lib/service-openapi';
import { getPublishedFooterItems } from '@/lib/navigation-tree';
import {
  DocsBody,
  DocsDescription,
  DocsPage,
  DocsTitle,
  MarkdownCopyButton,
  ViewOptionsPopover,
} from 'fumadocs-ui/layouts/docs/page';
import { createRelativeLink } from 'fumadocs-ui/mdx';
import { Clock3, MessageSquareWarning, PencilLine } from 'lucide-react';
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
      <DocsPage toc={[]} footer={{ enabled: false }} slots={{ container: DocsMainContainer }}>
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
  const openAPIGroup =
    domain.key === 'api' && slugs[0] === 'product-http'
      ? productHTTPOpenAPIReferenceGroups.find((group) => group.slug === slugs[1])
      : undefined;
  const serviceOpenAPIContract = serviceOpenAPIContractForSlugs([
    domain.key,
    ...slugs,
  ]);
  const githubContentPath = openAPIGroup
    ? productHTTPOpenAPIContractFiles[openAPIGroup.contract].source
    : serviceOpenAPIContract
      ? serviceOpenAPIContracts[serviceOpenAPIContract].source
      : page.data._openapi
        ? productHTTPOpenAPIContractFiles['golden-path'].source
        : `docs-site/content/docs/${page.path}`;
  const githubSourceUrl = `https://github.com/${gitConfig.user}/${gitConfig.repo}/blob/${gitConfig.branch}/${githubContentPath}`;
  const githubEditUrl = `https://github.com/${gitConfig.user}/${gitConfig.repo}/edit/${gitConfig.branch}/${githubContentPath}`;
  const feedbackUrl = getDocumentationFeedbackUrl({
    locale,
    pageTitle: page.data.title,
    pagePath: entry.url,
  });

  return (
    <DocsPage
      toc={page.data.toc}
      full={page.data.full}
      footer={{ items: getPublishedFooterItems(locale, domain.key, entry.url) }}
      slots={{ container: DocsMainContainer }}
    >
      <DocsTitle>{page.data.title}</DocsTitle>
      <DocsDescription className="mb-0">{page.data.description}</DocsDescription>
      <div
        className="flex flex-row flex-wrap items-center gap-2 border-b pb-6"
        role="group"
        aria-label={locale === 'zh' ? '页面操作' : 'Page actions'}
      >
        <MarkdownCopyButton markdownUrl={markdownUrl} />
        <ViewOptionsPopover markdownUrl={markdownUrl} githubUrl={githubSourceUrl} />
        <a
          href={githubEditUrl}
          target="_blank"
          rel="noreferrer noopener"
          className="inline-flex h-8 items-center gap-1.5 rounded-md border border-fd-border bg-fd-secondary px-3 text-sm font-medium transition-colors hover:bg-fd-accent"
        >
          <PencilLine className="size-3.5" aria-hidden="true" />
          {locale === 'zh' ? '编辑此页' : 'Edit this page'}
        </a>
        <a
          href={feedbackUrl}
          target="_blank"
          rel="noreferrer noopener"
          className="inline-flex h-8 items-center gap-1.5 rounded-md border border-fd-border bg-fd-secondary px-3 text-sm font-medium transition-colors hover:bg-fd-accent"
        >
          <MessageSquareWarning className="size-3.5" aria-hidden="true" />
          {locale === 'zh' ? '报告文档问题' : 'Report a docs issue'}
        </a>
      </div>
      <DocsBody>
        <MDX
          components={getMDXComponents({
            a: createRelativeLink(source, page),
            OpenAPIPage: async (props) => (
              <OpenAPIPage {...(await openapi.preloadOpenAPIPage(page))} {...props} />
            ),
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
      canonical: canonicalUrl(entry.url),
      languages: {
        zh: canonicalUrl(entry.url.replace(`/${locale}/`, '/zh/')),
        en: canonicalUrl(entry.url.replace(`/${locale}/`, '/en/')),
      },
    },
    robots: getRobotsMetadata(entry.status === 'published'),
    openGraph:
      entry.status === 'published'
        ? {
            type: 'article',
            url: canonicalUrl(entry.url),
            title: entry.label,
            description: entry.description,
          }
        : undefined,
  };
}
