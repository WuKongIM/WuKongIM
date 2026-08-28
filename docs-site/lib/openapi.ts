import { createOpenAPI } from 'fumadocs-openapi/server';
import {
  localizeOpenAPIDocument,
  productHTTPOpenAPIContractNames,
  productHTTPOpenAPIContracts,
  type ProductHTTPOpenAPIContract,
  type ProductHTTPOpenAPILocale,
} from './product-http-openapi';
import { serviceOpenAPISchemas } from './service-openapi';

export { localizeOpenAPIDocument } from './product-http-openapi';

type OpenAPIOptions = NonNullable<Parameters<typeof createOpenAPI>[0]>;
type OpenAPISchemaRecord = Exclude<NonNullable<OpenAPIOptions['input']>, string[]>;

/** Stable schema ID embedded into generated Product HTTP reference pages. */
export const productHTTPOpenAPIDocumentId =
  productHTTPOpenAPIContracts['golden-path'].documentId;

function localizedDocumentIds<const DocumentId extends string>(documentId: DocumentId) {
  return {
    zh: `${documentId}-zh` as const,
    en: `${documentId}-en` as const,
  } as const;
}

export const productHTTPOpenAPIDocumentIds = localizedDocumentIds(
  productHTTPOpenAPIDocumentId,
);

/** Stable schema ID embedded into the complete Product HTTP reference. */
export const productHTTPCompleteOpenAPIDocumentId =
  productHTTPOpenAPIContracts.complete.documentId;

export const productHTTPCompleteOpenAPIDocumentIds = localizedDocumentIds(
  productHTTPCompleteOpenAPIDocumentId,
);

/** Stable schema ID embedded into the generated trusted-management pages. */
export const productHTTPManagementOpenAPIDocumentId =
  productHTTPOpenAPIContracts.management.documentId;

export const productHTTPManagementOpenAPIDocumentIds = localizedDocumentIds(
  productHTTPManagementOpenAPIDocumentId,
);

/** Stable schema ID embedded into the generated message-sending pages. */
export const productHTTPMessagingOpenAPIDocumentId =
  productHTTPOpenAPIContracts.messaging.documentId;

export const productHTTPMessagingOpenAPIDocumentIds = localizedDocumentIds(
  productHTTPMessagingOpenAPIDocumentId,
);

function localizedDocumentId(
  contract: ProductHTTPOpenAPIContract,
  locale: ProductHTTPOpenAPILocale,
) {
  return `${productHTTPOpenAPIContracts[contract].documentId}-${locale}`;
}

function localizedContractDocument(
  contract: ProductHTTPOpenAPIContract,
  locale: ProductHTTPOpenAPILocale,
) {
  return localizeOpenAPIDocument(
    productHTTPOpenAPIContracts[contract].document,
    locale,
  ) as unknown as OpenAPISchemaRecord[string];
}

/** Creates the one-contract, one-locale source used by deterministic MDX generation. */
export function createProductHTTPOpenAPIContract(
  contract: ProductHTTPOpenAPIContract,
  locale: ProductHTTPOpenAPILocale,
) {
  return createOpenAPI({
    input: {
      [localizedDocumentId(contract, locale)]: localizedContractDocument(contract, locale),
    },
  });
}

/** Creates the one-locale source used by deterministic MDX generation. */
export function createProductHTTPOpenAPI(locale: ProductHTTPOpenAPILocale) {
  return createProductHTTPOpenAPIContract('golden-path', locale);
}

/** Creates the one-locale source used by deterministic management-page generation. */
export function createProductHTTPManagementOpenAPI(locale: ProductHTTPOpenAPILocale) {
  return createProductHTTPOpenAPIContract('management', locale);
}

/** Creates the one-locale source used by deterministic message-page generation. */
export function createProductHTTPMessagingOpenAPI(locale: ProductHTTPOpenAPILocale) {
  return createProductHTTPOpenAPIContract('messaging', locale);
}

const productHTTPOpenAPILocales: ProductHTTPOpenAPILocale[] = ['zh', 'en'];
const productHTTPOpenAPISchemas = Object.fromEntries(
  productHTTPOpenAPIContractNames.flatMap((contract) =>
    productHTTPOpenAPILocales.map((locale) => [
      localizedDocumentId(contract, locale),
      localizedContractDocument(contract, locale),
    ]),
  ),
) as OpenAPISchemaRecord;

/** Server-only loader for every published Product HTTP OpenAPI page. */
export const openapi = createOpenAPI({
  input: {
    ...productHTTPOpenAPISchemas,
    ...serviceOpenAPISchemas,
  } as OpenAPISchemaRecord,
});
