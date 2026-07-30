import type * as PageTree from 'fumadocs-core/page-tree';
import type { LayoutTab } from 'fumadocs-ui/layouts/shared';
import { domains, getAllNavigationEntries, type DocumentationDomain, type Locale } from './navigation';

function label(name: string, planned: boolean, locale: Locale) {
  if (!planned) return name;

  return (
    <span className="flex min-w-0 items-center gap-2">
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
): PageTree.Item {
  return {
    type: 'page',
    name: label(name, planned, locale),
    description,
    url: `/${[locale, domain.key, ...slugs].join('/')}`,
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
        [page.slug],
        page.label[locale],
        page.description[locale],
        page.status === 'planned',
      ),
    );
  }

  for (const group of domain.groups) {
    children.push({
      type: 'folder',
      name: label(group.label[locale], group.status === 'planned', locale),
      description: group.description[locale],
      defaultOpen: false,
      index: pageNode(
        locale,
        domain,
        [group.slug],
        group.label[locale],
        group.description[locale],
        group.status === 'planned',
      ),
      children: group.children.map((page) =>
        pageNode(
          locale,
          domain,
          [group.slug, page.slug],
          page.label[locale],
          page.description[locale],
          page.status === 'planned',
        ),
      ),
    });
  }

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
