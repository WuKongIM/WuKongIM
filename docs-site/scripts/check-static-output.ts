import { domains, getIndexedNavigationEntries, locales } from '../lib/navigation';

const out = new URL('../out/', import.meta.url);

async function text(path: string) {
  const file = Bun.file(new URL(path, out));
  if (!(await file.exists())) throw new Error(`missing static output: ${path}`);
  return file.text();
}

for (const locale of locales) {
  await text(`${locale}/index.html`);
  for (const domain of domains) {
    await text(`${locale}/${domain.key}/index.html`);
  }
}

const planned = await text('zh/guide/tutorials/direct-chat/index.html');
if (!planned.includes('<meta name="robots" content="noindex, follow"/>')) {
  throw new Error('planned pages must carry a noindex directive');
}

const published = await text('zh/guide/quick-start/first-message/index.html');
if (published.includes('<meta name="robots" content="noindex')) {
  throw new Error('published pages must be indexable');
}

const sitemap = await text('sitemap.xml');
const sitemapUrls = [...sitemap.matchAll(/<loc>(.*?)<\/loc>/g)].map((match) => match[1]);
const expectedSitemapPaths = locales.flatMap((locale) => [
  `/${locale}`,
  ...getIndexedNavigationEntries(locale).map((entry) => entry.url),
]);
const actualSitemapPaths = sitemapUrls.map((url) => new URL(url).pathname);
if (actualSitemapPaths.sort().join('\n') !== expectedSitemapPaths.sort().join('\n')) {
  throw new Error(
    `sitemap routes differ from the publication registry:\n${actualSitemapPaths.join('\n')}`,
  );
}
if (sitemap.includes('/tutorials/direct-chat')) {
  throw new Error('planned pages must not appear in sitemap.xml');
}

const llms = `${await text('llms.txt')}\n${await text('llms-full.txt')}`;
for (const locale of locales) {
  for (const entry of getIndexedNavigationEntries(locale)) {
    if (!llms.includes(entry.url)) {
      throw new Error(`missing published LLM route: ${entry.url}`);
    }
  }
}
if (llms.includes('/tutorials/direct-chat')) {
  throw new Error('planned pages must not appear in LLM outputs');
}

const search = JSON.parse(await text('api/search')) as {
  type: string;
  data: Record<
    string,
    { internalDocumentIDStore: { internalIdToId: string[] } }
  >;
};
if (search.type !== 'i18n') throw new Error('search index must be locale-aware');
if (Object.keys(search.data).sort().join(',') !== 'en,zh') {
  throw new Error('search index must contain exactly the en and zh locales');
}
for (const locale of locales) {
  const ids = search.data[locale]?.internalDocumentIDStore.internalIdToId ?? [];
  if (ids.length === 0 || ids.some((id) => !id.startsWith(`/${locale}/`))) {
    throw new Error(`${locale} search index contains cross-language documents`);
  }
  for (const entry of getIndexedNavigationEntries(locale)) {
    if (!ids.includes(entry.url)) {
      throw new Error(`${locale} search index is missing ${entry.url}`);
    }
  }
  if (ids.some((id) => id.includes('/tutorials/direct-chat'))) {
    throw new Error(`${locale} search index contains a planned page`);
  }
}

console.log('static output contract passed');
