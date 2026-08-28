import openapiDocument from '../contracts/javascript-web-quickstart.openapi.json';
import managementOpenAPIDocument from '../contracts/product-http-management.openapi.json';
import { createOpenAPI } from 'fumadocs-openapi/server';
import {
  localizeOpenAPIDocument,
  type ProductHTTPOpenAPILocale,
} from './product-http-openapi';

export { localizeOpenAPIDocument } from './product-http-openapi';

type OpenAPIOptions = NonNullable<Parameters<typeof createOpenAPI>[0]>;
type OpenAPISchemaRecord = Exclude<NonNullable<OpenAPIOptions['input']>, string[]>;

/** Stable schema ID embedded into generated Product HTTP reference pages. */
export const productHTTPOpenAPIDocumentId = 'wukongim-product-http-beta';

export const productHTTPOpenAPIDocumentIds = {
  zh: `${productHTTPOpenAPIDocumentId}-zh`,
  en: `${productHTTPOpenAPIDocumentId}-en`,
} as const;

/** Stable schema ID embedded into the generated trusted-management pages. */
export const productHTTPManagementOpenAPIDocumentId =
  'wukongim-product-http-management-beta';

export const productHTTPManagementOpenAPIDocumentIds = {
  zh: `${productHTTPManagementOpenAPIDocumentId}-zh`,
  en: `${productHTTPManagementOpenAPIDocumentId}-en`,
} as const;

function localizedDocument(locale: ProductHTTPOpenAPILocale) {
  return localizeOpenAPIDocument(openapiDocument, locale) as unknown as OpenAPISchemaRecord[string];
}

function localizedManagementDocument(locale: ProductHTTPOpenAPILocale) {
  return localizeOpenAPIDocument(
    managementOpenAPIDocument,
    locale,
  ) as unknown as OpenAPISchemaRecord[string];
}

/** Creates the one-locale source used by deterministic MDX generation. */
export function createProductHTTPOpenAPI(locale: ProductHTTPOpenAPILocale) {
  return createOpenAPI({
    input: {
      [productHTTPOpenAPIDocumentIds[locale]]: localizedDocument(locale),
    },
  });
}

/** Creates the one-locale source used by deterministic management-page generation. */
export function createProductHTTPManagementOpenAPI(locale: ProductHTTPOpenAPILocale) {
  return createOpenAPI({
    input: {
      [productHTTPManagementOpenAPIDocumentIds[locale]]:
        localizedManagementDocument(locale),
    },
  });
}

/** Server-only loader for all published golden-path and management OpenAPI pages. */
export const openapi = createOpenAPI({
  input: {
    [productHTTPOpenAPIDocumentIds.zh]: localizedDocument('zh'),
    [productHTTPOpenAPIDocumentIds.en]: localizedDocument('en'),
    [productHTTPManagementOpenAPIDocumentIds.zh]: localizedManagementDocument('zh'),
    [productHTTPManagementOpenAPIDocumentIds.en]: localizedManagementDocument('en'),
  },
});
