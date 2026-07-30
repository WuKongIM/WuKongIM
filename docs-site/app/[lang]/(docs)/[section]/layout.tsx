import { baseOptions } from '@/lib/layout.shared';
import { domains, parseLocale, type DocumentationDomain } from '@/lib/navigation';
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
  const locale = parseLocale(values.lang);
  const domain = domains.find((candidate) => candidate.key === values.section) as
    | DocumentationDomain
    | undefined;
  if (!locale || !domain) notFound();

  return (
    <DocsLayout
      tree={buildPageTree(locale, domain.key)}
      tabs={buildLayoutTabs(locale)}
      tabMode="auto"
      sidebar={{ defaultOpenLevel: 1, prefetch: false }}
      {...baseOptions(locale)}
    >
      {children}
    </DocsLayout>
  );
}
