import { isPreviewBuild, siteUrl } from '@/lib/shared';
import type { MetadataRoute } from 'next';

export const dynamic = 'force-static';

export default function robots(): MetadataRoute.Robots {
  const preview = isPreviewBuild();

  return {
    rules: preview
      ? { userAgent: '*', disallow: '/' }
      : {
          userAgent: '*',
          allow: '/',
        },
    sitemap: new URL('/sitemap.xml', siteUrl).toString(),
  };
}
