import { locales } from '@/lib/navigation';
import { siteUrl } from '@/lib/shared';
import { source } from '@/lib/source';
import type { MetadataRoute } from 'next';

export const dynamic = 'force-static';

export default function sitemap(): MetadataRoute.Sitemap {
  const now = new Date();
  const homePages = locales.map((locale) => ({
    url: new URL(`/${locale}`, siteUrl).toString(),
    lastModified: now,
    changeFrequency: 'weekly' as const,
    priority: 1,
  }));
  const publishedPages = source.getPages().map((page) => ({
    url: new URL(page.url, siteUrl).toString(),
    lastModified: now,
    changeFrequency: 'weekly' as const,
    priority: 0.8,
  }));

  return [...homePages, ...publishedPages];
}
