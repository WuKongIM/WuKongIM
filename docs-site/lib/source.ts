import { docs } from 'collections/server';
import { loader } from 'fumadocs-core/source';
import { i18n } from './i18n';
import { isPublishedContentPath } from './navigation';
import { docsContentRoute } from './shared';
import { renderDeveloperContractSupplement } from './developer-contracts';

const generatedSource = docs.toFumadocsSource();

export const source = loader({
  baseUrl: '/',
  source: {
    ...generatedSource,
    files: generatedSource.files.filter(
      (file) => file.type !== 'page' || isPublishedContentPath(file.path),
    ),
  },
  i18n,
  url(slugs, locale) {
    return `/${[locale, ...slugs].filter(Boolean).join('/')}`;
  },
  plugins: [],
});

export function getPageMarkdownUrl(page: (typeof source)['$inferPage']) {
  const segments = [page.locale, ...page.slugs, 'content.md'].filter(Boolean) as string[];

  return {
    segments,
    url: '/' + [...docsContentRoute.split('/'), ...segments].filter(Boolean).join('/'),
  };
}

export async function getLLMText(page: (typeof source)['$inferPage']) {
  const processed = await page.data.getText('processed');
  const locale = page.locale === 'zh' || page.locale === 'en' ? page.locale : undefined;
  const supplement = locale ? renderDeveloperContractSupplement(locale, page.slugs) : '';

  return `# ${page.data.title} (${page.url})

${processed}${supplement ? `\n\n${supplement}` : ''}`;
}
