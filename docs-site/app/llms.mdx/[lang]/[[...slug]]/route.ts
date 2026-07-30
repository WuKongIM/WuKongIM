import { getLLMText, getPageMarkdownUrl, source } from '@/lib/source';
import { locales } from '@/lib/navigation';
import { notFound } from 'next/navigation';

export const revalidate = false;

export async function GET(
  _request: Request,
  { params }: { params: Promise<{ lang: string; slug?: string[] }> },
) {
  const { lang, slug } = await params;
  const locale = locales.find((candidate) => candidate === lang);
  if (!locale || !slug?.length) notFound();

  const page = source.getPage(slug.slice(0, -1), locale);
  if (!page || slug.at(-1) !== 'content.md') notFound();

  return new Response(await getLLMText(page), {
    headers: {
      'Content-Type': 'text/markdown; charset=utf-8',
    },
  });
}

export function generateStaticParams() {
  return source.getPages().map((page) => {
    const [lang, ...slug] = getPageMarkdownUrl(page).segments;
    return { lang, slug };
  });
}
