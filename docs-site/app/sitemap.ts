import { locales } from '@/lib/navigation';
import { canonicalUrl } from '@/lib/shared';
import { source } from '@/lib/source';
import type { MetadataRoute } from 'next';

export const dynamic = 'force-static';

export default function sitemap(): MetadataRoute.Sitemap {
  const homePages = locales.map((locale) => ({
    url: canonicalUrl(`/${locale}`),
    changeFrequency: 'weekly' as const,
    priority: 1,
  }));
  const publishedPages = source.getPages().map((page) => ({
    url: canonicalUrl(page.url),
    changeFrequency: 'weekly' as const,
    priority: 0.8,
  }));

  return [...homePages, ...publishedPages];
}
