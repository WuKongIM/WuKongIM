import operationsDocument from '../contracts/operations-http.openapi.json';
import webhooksDocument from '../contracts/webhooks.openapi.json';
import {
  localizeOpenAPIDocument,
  type ProductHTTPOpenAPILocale,
} from './product-http-openapi';

export type ServiceOpenAPIContract = 'operations' | 'webhooks';

interface ServiceOpenAPIContractDescriptor {
  document: object;
  source: string;
  download: string;
  documentId: string;
}

/** Non-Product OpenAPI contracts rendered by the shared Fumadocs server. */
export const serviceOpenAPIContracts = {
  operations: {
    document: operationsDocument,
    source: 'docs-site/contracts/operations-http.openapi.json',
    download: '/contracts/operations-http.openapi.json',
    documentId: 'wukongim-operations-http-beta',
  },
  webhooks: {
    document: webhooksDocument,
    source: 'docs-site/contracts/webhooks.openapi.json',
    download: '/contracts/webhooks.openapi.json',
    documentId: 'wukongim-webhooks-beta',
  },
} as const satisfies Record<ServiceOpenAPIContract, ServiceOpenAPIContractDescriptor>;

export function localizedServiceOpenAPIDocumentId(
  contract: ServiceOpenAPIContract,
  locale: ProductHTTPOpenAPILocale,
) {
  return `${serviceOpenAPIContracts[contract].documentId}-${locale}`;
}

/** Schema record merged into the one server-only Fumadocs OpenAPI loader. */
export const serviceOpenAPISchemas = Object.fromEntries(
  (Object.keys(serviceOpenAPIContracts) as ServiceOpenAPIContract[]).flatMap(
    (contract) =>
      (['zh', 'en'] as const).map((locale) => [
        localizedServiceOpenAPIDocumentId(contract, locale),
        localizeOpenAPIDocument(
          serviceOpenAPIContracts[contract].document,
          locale,
        ),
      ]),
  ),
);

/** Resolves the contract-owned edit source for hand-authored OpenAPI pages. */
export function serviceOpenAPIContractForSlugs(
  slugs: readonly string[],
): ServiceOpenAPIContract | undefined {
  if (slugs[0] !== 'api') return undefined;
  if (slugs[1] === 'operations-http' && slugs[2] !== 'stability') {
    return 'operations';
  }
  if (slugs[1] === 'webhooks' && slugs[2] === 'payloads') {
    return 'webhooks';
  }
  return undefined;
}
