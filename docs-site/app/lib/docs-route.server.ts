import { source } from '@/lib/source';

export async function loadDocsPage(slugs: string[]) {
  const page = source.getPage(slugs);
  if (!page) throw new Response('Not found', { status: 404 });

  return {
    path: page.path,
    pageTree: await source.serializePageTree(source.getPageTree()),
  };
}
