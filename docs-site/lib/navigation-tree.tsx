import type * as PageTree from 'fumadocs-core/page-tree';
import type { LayoutTab } from 'fumadocs-ui/layouts/shared';
import {
  domains,
  getAllNavigationEntries,
  getIndexedNavigationEntries,
  isNavigationGroup,
  navigationChildParentSlugs,
  navigationPathSegments,
  type DocumentationDomain,
  type Locale,
  type NavigationNode,
} from './navigation';

export interface DomainPublicationCounts {
  published: number;
  planned: number;
  total: number;
}

interface FooterItem {
  name: string;
  description: string;
  url: string;
}

export interface PublishedFooterItems {
  previous?: FooterItem;
  next?: FooterItem;
}

function label(name: string, planned: boolean, locale: Locale) {
  if (!planned) return name;

  return (
    <span key={`planned:${locale}:${name}`} className="flex min-w-0 items-center gap-2">
      <span className="truncate">{name}</span>
      <span className="rounded-full border border-fd-border bg-fd-muted px-1.5 py-0.5 text-[9px] font-semibold uppercase tracking-wide text-fd-muted-foreground">
        {locale === 'zh' ? '规划中' : 'Planned'}
      </span>
    </span>
  );
}

function pageNode(
  locale: Locale,
  domain: DocumentationDomain,
  slugs: string[],
  name: string,
  description: string,
  planned: boolean,
  method?: string,
): PageTree.Item {
  const nameLabel = label(name, planned, locale);
  const methodColor =
    method === 'POST'
      ? 'text-blue-600 dark:text-blue-400'
      : method === 'DELETE'
        ? 'text-red-600 dark:text-red-400'
        : method === 'PUT'
          ? 'text-yellow-600 dark:text-yellow-400'
          : method === 'PATCH'
            ? 'text-orange-600 dark:text-orange-400'
            : 'text-green-600 dark:text-green-400';
  return {
    type: 'page',
    name: method ? (
      <span className="flex min-w-0 flex-1 items-center gap-2">
        <span className="truncate">{nameLabel}</span>
        <span className={`ms-auto font-mono text-xs font-medium text-nowrap ${methodColor}`}>
          {method}
        </span>
      </span>
    ) : (
      nameLabel
    ),
    description,
    url: `/${[locale, domain.key, ...slugs].join('/')}`,
  };
}

function navigationNode(
  locale: Locale,
  domain: DocumentationDomain,
  node: NavigationNode,
  parentSlugs: string[],
): PageTree.Node {
  const slugs = [...parentSlugs, ...navigationPathSegments(node.slug)];
  if (!isNavigationGroup(node)) {
    return pageNode(
      locale,
      domain,
      slugs,
      node.label[locale],
      node.description[locale],
      node.status === 'planned',
      node.method,
    );
  }

  return {
    type: 'folder',
    name: label(node.label[locale], node.status === 'planned', locale),
    description: node.description[locale],
    defaultOpen: false,
    index: pageNode(
      locale,
      domain,
      slugs,
      node.label[locale],
      node.description[locale],
      node.status === 'planned',
    ),
    children: node.children.map((child) =>
      navigationNode(locale, domain, child, navigationChildParentSlugs(node, slugs)),
    ),
  };
}

/** Builds the sidebar tree for one top-level documentation domain. */
export function buildPageTree(
  locale: Locale,
  domainKey: DocumentationDomain['key'],
): PageTree.Root {
  const domain = domains.find((candidate) => candidate.key === domainKey);
  if (!domain) throw new Error(`unknown documentation domain: ${domainKey}`);

  const children: PageTree.Node[] = [
    pageNode(
      locale,
      domain,
      [],
      locale === 'zh' ? '概览' : 'Overview',
      domain.description[locale],
      false,
    ),
  ];

  for (const page of domain.pages) {
    children.push(
      pageNode(
        locale,
        domain,
        navigationPathSegments(page.slug),
        page.label[locale],
        page.description[locale],
        page.status === 'planned',
      ),
    );
  }

  children.push(...domain.groups.map((group) => navigationNode(locale, domain, group, [])));

  return {
    type: 'root',
    name: domain.label[locale],
    description: domain.description[locale],
    children,
  };
}

/** Builds the four globally visible Fumadocs layout tabs. */
export function buildLayoutTabs(locale: Locale): LayoutTab[] {
  return domains.map((domain) => ({
    title: domain.label[locale],
    description: domain.description[locale],
    url: `/${locale}/${domain.key}`,
    urls: new Set(
      getAllNavigationEntries(locale)
        .filter((entry) => entry.domain === domain.key)
        .map((entry) => entry.url),
    ),
  }));
}

/** Counts addressable routes by their actual publication state for one domain. */
export function getDomainPublicationCounts(
  locale: Locale,
  domainKey: DocumentationDomain['key'],
): DomainPublicationCounts {
  const entries = getAllNavigationEntries(locale).filter((entry) => entry.domain === domainKey);
  const published = entries.filter((entry) => entry.status === 'published').length;
  const planned = entries.length - published;

  return { published, planned, total: entries.length };
}

/** Resolves published-only neighbours for the page footer within its current domain. */
export function getPublishedFooterItems(
  locale: Locale,
  domainKey: DocumentationDomain['key'],
  pageUrl: string,
): PublishedFooterItems {
  const entries = getIndexedNavigationEntries(locale).filter(
    (entry) => entry.domain === domainKey,
  );
  const normalizedPageUrl = pageUrl.replace(/\/+$/, '');
  const index = entries.findIndex((entry) => entry.url === normalizedPageUrl);
  if (index === -1) return {};

  const toFooterItem = (entry: (typeof entries)[number] | undefined): FooterItem | undefined =>
    entry
      ? {
          name: entry.label,
          description: entry.description,
          url: entry.url,
        }
      : undefined;

  return {
    previous: toFooterItem(entries[index - 1]),
    next: toFooterItem(entries[index + 1]),
  };
}
