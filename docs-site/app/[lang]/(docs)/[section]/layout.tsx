import { baseOptions } from '@/lib/layout.shared';
import { domains, locales, type DocumentationDomain, type Locale } from '@/lib/navigation';
import { buildLayoutTabs, buildPageTree } from '@/lib/navigation-tree';
import { DocsLayout } from 'fumadocs-ui/layouts/docs';
import { notFound } from 'next/navigation';
import type { ReactNode } from 'react';

export default async function DocumentationLayout({
  children,
  params,
}: {
  children: ReactNode;
  params: Promise<{ lang: string; section: string }>;
}) {
  const values = await params;
  const locale = locales.find((candidate) => candidate === values.lang) as Locale | undefined;
  const domain = domains.find((candidate) => candidate.key === values.section) as
    | DocumentationDomain
    | undefined;
  if (!locale || !domain) notFound();

  return (
    <DocsLayout
      tree={buildPageTree(locale, domain.key)}
      tabs={buildLayoutTabs(locale)}
      tabMode="top"
      sidebar={{ defaultOpenLevel: 1, prefetch: false }}
      {...baseOptions(locale)}
    >
      {children}
    </DocsLayout>
  );
}
