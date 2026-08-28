import { docs } from 'collections/server';
import { loader } from 'fumadocs-core/source';
import { i18n } from './i18n';
import { isPublishedContentPath } from './navigation';
import { openapi } from './openapi';
import { renderOpenAPIOperationMarkdown } from './openapi-markdown';
import { docsContentRoute } from './shared';
import { renderDeveloperContractSupplement } from './developer-contracts';
import { renderClientProtocolPacketMarkdown } from './client-protocol-contracts';

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
  plugins: [openapi.loaderPlugin()],
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
  const supplement = locale
      ? [
        renderDeveloperContractSupplement(locale, page.slugs),
        page.slugs.join('/') === 'api/client-protocols/packet-types'
          ? renderClientProtocolPacketMarkdown(locale)
          : '',
        renderOpenAPIOperationMarkdown(locale, page.slugs),
      ]
        .filter(Boolean)
        .join('\n\n')
    : '';

  return `# ${page.data.title} (${page.url})

${processed}${supplement ? `\n\n${supplement}` : ''}`;
}
