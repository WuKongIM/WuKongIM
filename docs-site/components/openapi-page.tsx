'use client';

import { createCodeUsageGeneratorRegistry } from 'fumadocs-openapi/requests/generators';
import { createOpenAPIPage } from 'fumadocs-openapi/ui';

// Only contract-owned x-codeSamples are rendered. The default browser-oriented
// generators would obscure the trusted-backend boundary of Product HTTP.
const codeUsages = createCodeUsageGeneratorRegistry();

/** Renders static-export-safe API reference content without an HTTP playground. */
export const OpenAPIPage = createOpenAPIPage({
  codeUsages,
  playground: { enabled: false },
  schemaUI: { showExample: true },
});
