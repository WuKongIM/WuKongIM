import type { Locale } from './navigation';
import {
  localizeOpenAPIDocument,
  productHTTPOpenAPIContracts,
  productHTTPOpenAPIReferenceOperations,
} from './product-http-openapi';

interface SchemaObject {
  $ref?: string;
  type?: string | string[];
  format?: string;
  contentEncoding?: string;
  description?: string;
  default?: unknown;
  const?: unknown;
  enum?: unknown[];
  example?: unknown;
  examples?: unknown[];
  minimum?: number;
  maximum?: number;
  minLength?: number;
  minItems?: number;
  maxItems?: number;
  pattern?: string;
  additionalProperties?: boolean | SchemaObject;
  required?: string[];
  properties?: Record<string, SchemaObject>;
  items?: SchemaObject;
  oneOf?: SchemaObject[];
  anyOf?: SchemaObject[];
  allOf?: SchemaObject[];
}

interface ExampleObject {
  value?: unknown;
}

interface MediaObject {
  schema?: SchemaObject;
  example?: unknown;
  examples?: Record<string, ExampleObject>;
}

interface ResponseObject {
  $ref?: string;
  description?: string;
  content?: Record<string, MediaObject>;
}

interface CodeSample {
  lang?: string;
  label?: string;
  source?: string;
}

interface OperationObject {
  operationId?: string;
  summary?: string;
  description?: string;
  'x-codeSamples'?: CodeSample[];
  requestBody?: {
    content?: Record<string, MediaObject>;
  };
  responses?: Record<string, ResponseObject>;
}

interface ContractDocument {
  paths: Record<string, Record<string, OperationObject>>;
  components?: {
    schemas?: Record<string, SchemaObject>;
    responses?: Record<string, ResponseObject>;
  };
}

interface NamedSchema {
  name: string;
  schema: SchemaObject;
}

function localReferenceName(reference: string, group: 'schemas' | 'responses') {
  const prefix = `#/components/${group}/`;
  return reference.startsWith(prefix) ? reference.slice(prefix.length) : undefined;
}

function schemaReferenceName(schema: SchemaObject | undefined) {
  return schema?.$ref ? localReferenceName(schema.$ref, 'schemas') : undefined;
}

function resolveSchema(document: ContractDocument, schema: SchemaObject | undefined) {
  const name = schemaReferenceName(schema);
  const referenced = name ? document.components?.schemas?.[name] : undefined;
  if (!referenced || !schema) return referenced ?? schema;
  return schema.description ? { ...referenced, description: schema.description } : referenced;
}

function resolveResponse(document: ContractDocument, response: ResponseObject) {
  const name = response.$ref ? localReferenceName(response.$ref, 'responses') : undefined;
  return name ? document.components?.responses?.[name] : response;
}

function markdownCell(value: unknown) {
  return String(value ?? '—')
    .replaceAll('|', '\\|')
    .replaceAll('\n', ' ');
}

function jsonValue(value: unknown) {
  return JSON.stringify(value) ?? String(value);
}

function schemaType(schema: SchemaObject): string {
  if (schema.$ref) return `\`${schemaReferenceName(schema) ?? schema.$ref}\``;
  for (const alternatives of [schema.oneOf, schema.anyOf]) {
    if (alternatives) return alternatives.map(schemaType).join(' | ');
  }
  if (schema.allOf) return schema.allOf.map(schemaType).join(' & ');
  if (schema.type === 'array' && schema.items) {
    return `array<${schemaType(schema.items)}>`;
  }
  const type = Array.isArray(schema.type) ? schema.type.join(' | ') : schema.type;
  return `\`${type ?? 'unknown'}${schema.format ? `:${schema.format}` : ''}\``;
}

function schemaConstraints(schema: SchemaObject) {
  const constraints: string[] = [];
  if (schema.const !== undefined) constraints.push(`const: \`${jsonValue(schema.const)}\``);
  if (schema.enum) {
    constraints.push(`enum: ${schema.enum.map((item) => `\`${jsonValue(item)}\``).join(', ')}`);
  }
  if (schema.minimum !== undefined || schema.maximum !== undefined) {
    constraints.push(`${schema.minimum ?? '−∞'}–${schema.maximum ?? '∞'}`);
  }
  if (schema.minLength !== undefined) constraints.push(`minLength: ${schema.minLength}`);
  if (schema.minItems !== undefined) constraints.push(`minItems: ${schema.minItems}`);
  if (schema.maxItems !== undefined) constraints.push(`maxItems: ${schema.maxItems}`);
  if (schema.pattern !== undefined) constraints.push(`pattern: \`${schema.pattern}\``);
  if (schema.default !== undefined) {
    constraints.push(`default: \`${jsonValue(schema.default)}\``);
  }
  if (schema.contentEncoding) constraints.push(`encoding: ${schema.contentEncoding}`);
  if (schema.additionalProperties === false) constraints.push('additionalProperties: `false`');
  if (schema.example !== undefined) constraints.push(`example: \`${jsonValue(schema.example)}\``);
  if (schema.examples) {
    constraints.push(`examples: ${schema.examples.map((item) => `\`${jsonValue(item)}\``).join(', ')}`);
  }
  return constraints.join('; ') || '—';
}

function renderSchemaFields(
  document: ContractDocument,
  locale: Locale,
  schema: SchemaObject | undefined,
) {
  const resolved = resolveSchema(document, schema);
  if (!resolved?.properties) return '';
  const required = new Set(resolved.required ?? []);
  const headings =
    locale === 'zh'
      ? ['字段', '类型', '必填', '约束', '说明']
      : ['Field', 'Type', 'Required', 'Constraints', 'Description'];
  const rows = Object.entries(resolved.properties).map(([name, field]) => {
    const resolvedField = resolveSchema(document, field) ?? field;
    const requiredLabel = required.has(name)
      ? locale === 'zh'
        ? '是'
        : 'yes'
      : locale === 'zh'
        ? '否'
        : 'no';
    return `| \`${name}\` | ${schemaType(field)} | ${requiredLabel} | ${markdownCell(schemaConstraints(resolvedField))} | ${markdownCell(resolvedField.description)} |`;
  });

  return [
    `| ${headings.join(' | ')} |`,
    `| ${headings.map(() => '---').join(' | ')} |`,
    ...rows,
  ].join('\n');
}

function collectReferencedSchemas(
  document: ContractDocument,
  schema: SchemaObject | undefined,
) {
  const rootName = schemaReferenceName(schema);
  const seen = new Set(rootName ? [rootName] : []);
  const result: NamedSchema[] = [];

  function visit(candidate: SchemaObject | undefined) {
    if (!candidate) return;
    const name = schemaReferenceName(candidate);
    const resolved = resolveSchema(document, candidate);
    if (!resolved) return;

    if (name && !seen.has(name)) {
      seen.add(name);
      result.push({ name, schema: resolved });
    }
    for (const field of Object.values(resolved.properties ?? {})) visit(field);
    visit(resolved.items);
    for (const item of resolved.oneOf ?? []) visit(item);
    for (const item of resolved.anyOf ?? []) visit(item);
    for (const item of resolved.allOf ?? []) visit(item);
    if (typeof resolved.additionalProperties === 'object') visit(resolved.additionalProperties);
  }

  const root = resolveSchema(document, schema);
  for (const field of Object.values(root?.properties ?? {})) visit(field);
  visit(root?.items);
  for (const item of root?.oneOf ?? []) visit(item);
  for (const item of root?.anyOf ?? []) visit(item);
  for (const item of root?.allOf ?? []) visit(item);
  return result;
}

function schemaDetails(
  document: ContractDocument,
  locale: Locale,
  schema: SchemaObject,
) {
  const resolved = resolveSchema(document, schema) ?? schema;
  const details: string[] = [];
  if (resolved.description) details.push(resolved.description, '');
  if (resolved.additionalProperties !== undefined) {
    const value =
      typeof resolved.additionalProperties === 'boolean'
        ? `\`${resolved.additionalProperties}\``
        : schemaType(resolved.additionalProperties);
    details.push(
      `- ${locale === 'zh' ? '允许额外属性' : 'Additional properties'}: ${value}`,
      '',
    );
  }
  const fields = renderSchemaFields(document, locale, schema);
  if (fields) details.push(fields);
  return details;
}

function appendSchemaTree(
  lines: string[],
  document: ContractDocument,
  locale: Locale,
  schema: SchemaObject | undefined,
  label: string,
  renderedNames: Set<string>,
  headingLevel = 4,
) {
  if (!schema) return;
  const name = schemaReferenceName(schema);
  if (name && renderedNames.has(name)) return;
  if (name) renderedNames.add(name);

  lines.push('', `${'#'.repeat(headingLevel)} ${label}${name ? ` — \`${name}\`` : ''}`, '');
  lines.push(...schemaDetails(document, locale, schema));

  for (const referenced of collectReferencedSchemas(document, schema)) {
    if (renderedNames.has(referenced.name)) continue;
    renderedNames.add(referenced.name);
    lines.push(
      '',
      `${'#'.repeat(headingLevel + 1)} ${locale === 'zh' ? '引用 Schema' : 'Referenced schema'} — \`${referenced.name}\``,
      '',
      ...schemaDetails(document, locale, referenced.schema),
    );
  }
}

function mediaExamples(media: MediaObject | undefined) {
  const examples: Array<{ name: string; value: unknown }> = [];
  if (media?.example !== undefined) examples.push({ name: 'default', value: media.example });
  for (const [name, example] of Object.entries(media?.examples ?? {})) {
    if (example.value !== undefined) examples.push({ name, value: example.value });
  }
  return examples;
}

function appendExamples(
  lines: string[],
  label: string,
  media: MediaObject | undefined,
  headingLevel = 4,
) {
  const examples = mediaExamples(media);
  if (examples.length === 0) return;
  lines.push('', `${'#'.repeat(headingLevel)} ${label}`, '');
  for (const example of examples) {
    lines.push(
      `${'#'.repeat(headingLevel + 1)} ${example.name}`,
      '',
      '```json',
      JSON.stringify(example.value, null, 2),
      '```',
      '',
    );
  }
}

function resolvePublishedPage(locale: Locale, slugs: readonly string[]) {
  if (slugs.length !== 4 || slugs[0] !== 'api' || slugs[1] !== 'product-http') {
    return undefined;
  }
  const publishedOperation = productHTTPOpenAPIReferenceOperations.find(
    (operation) => operation.groupSlug === slugs[2] && operation.slug === slugs[3],
  );
  if (!publishedOperation) return undefined;
  const contract = publishedOperation.contract;
  const descriptor = productHTTPOpenAPIContracts[contract];
  return {
    document: localizeOpenAPIDocument(
      descriptor.document,
      locale,
    ) as unknown as ContractDocument,
    operations: [publishedOperation],
    scope: descriptor.llmScope[locale],
    contractPath: descriptor.download,
    contractLabel: descriptor.label[locale],
  };
}

function appendSchemaSearchFacts(
  facts: Set<string>,
  document: ContractDocument,
  schema: SchemaObject | undefined,
) {
  const resolved = resolveSchema(document, schema);
  if (!resolved) return;

  const schemas: NamedSchema[] = [
    { name: schemaReferenceName(schema) ?? '', schema: resolved },
    ...collectReferencedSchemas(document, schema),
  ];
  for (const entry of schemas) {
    if (entry.name) facts.add(entry.name);
    if (entry.schema.description) facts.add(entry.schema.description);
    if (entry.schema.additionalProperties !== undefined) {
      facts.add(
        `additionalProperties ${
          typeof entry.schema.additionalProperties === 'boolean'
            ? entry.schema.additionalProperties
            : schemaType(entry.schema.additionalProperties)
        }`,
      );
    }
    for (const [name, field] of Object.entries(entry.schema.properties ?? {})) {
      const resolvedField = resolveSchema(document, field) ?? field;
      facts.add(
        [name, schemaType(field), schemaConstraints(resolvedField), resolvedField.description]
          .filter(Boolean)
          .join(' '),
      );
    }
  }
}

/** Returns source-derived request, response, nested-schema, and error text for search. */
export function renderOpenAPISearchText(locale: Locale, slugs: readonly string[]) {
  const page = resolvePublishedPage(locale, slugs);
  if (!page) return '';

  const facts = new Set<string>();
  for (const publishedOperation of page.operations) {
    const operation = page.document.paths[publishedOperation.path]?.[publishedOperation.method];
    if (!operation) continue;
    facts.add(`${publishedOperation.method.toUpperCase()} ${publishedOperation.path}`);
    if (operation.operationId) facts.add(operation.operationId);
    if (operation.summary) facts.add(operation.summary);
    if (operation.description) facts.add(operation.description);

    const requestMedia = operation.requestBody?.content?.['application/json'];
    appendSchemaSearchFacts(facts, page.document, requestMedia?.schema);
    for (const example of mediaExamples(requestMedia)) {
      facts.add(JSON.stringify(example.value));
    }

    for (const [status, item] of Object.entries(operation.responses ?? {})) {
      const response = resolveResponse(page.document, item);
      facts.add(`${status} ${response?.description ?? ''}`.trim());
      const responseMedia = response?.content?.['application/json'];
      appendSchemaSearchFacts(facts, page.document, responseMedia?.schema);
      for (const example of mediaExamples(responseMedia)) {
        facts.add(JSON.stringify(example.value));
      }
    }
  }
  return [...facts].join('\n');
}

function operationHeadingId(summary: string, seen: Map<string, number>) {
  const base = summary
    .toLowerCase()
    .replace(/[^\p{L}\p{N} _-]/gu, '')
    .replaceAll(' ', '-');
  const count = seen.get(base) ?? 0;
  seen.set(base, count + 1);
  return count === 0 ? base : `${base}-${count}`;
}

/** Extends Fumadocs' operation-only index data with the complete published schema facts. */
export function getOpenAPISearchStructuredData(locale: Locale, slugs: readonly string[]) {
  const page = resolvePublishedPage(locale, slugs);
  if (!page) return undefined;

  const seenHeadings = new Map<string, number>();
  const headings: Array<{ content: string; id: string }> = [];
  const contents: Array<{ content: string; heading?: string }> = [];
  for (const publishedOperation of page.operations) {
    const operation = page.document.paths[publishedOperation.path]?.[publishedOperation.method];
    if (!operation) continue;
    const title = operation.summary ?? operation.operationId ?? publishedOperation.path;
    const id = operationHeadingId(title, seenHeadings);
    headings.push({ content: title, id });
    if (operation.description) contents.push({ content: operation.description, heading: id });
  }

  const searchText = renderOpenAPISearchText(locale, slugs);
  if (searchText) contents.push({ content: searchText });
  return { headings, contents };
}

/** Renders the one operation on a published Fumadocs OpenAPI page. */
export function renderOpenAPIOperationMarkdown(locale: Locale, slugs: readonly string[]) {
  const page = resolvePublishedPage(locale, slugs);
  if (!page) return '';

  const { document } = page;
  const lines = [
    `## ${locale === 'zh' ? 'OpenAPI 3.1 操作合同' : 'OpenAPI 3.1 operation contract'}`,
    '',
    page.scope,
  ];

  for (const publishedOperation of page.operations) {
    const operation = document.paths[publishedOperation.path]?.[publishedOperation.method];
    if (!operation) continue;
    const requestMedia = operation.requestBody?.content?.['application/json'];
    const responseEntries = Object.entries(operation.responses ?? {}).map(([status, item]) => {
      const response = resolveResponse(document, item);
      return { status, response, media: response?.content?.['application/json'] };
    });
    lines.push(
      '',
      `### \`${publishedOperation.method.toUpperCase()}\` \`${publishedOperation.path}\``,
      '',
      `- Operation ID: \`${operation.operationId ?? '—'}\``,
      '',
      operation.description ?? '',
    );

    const codeSamples = operation['x-codeSamples'] ?? [];
    if (codeSamples.length > 0) {
      lines.push(
        '',
        `#### ${locale === 'zh' ? '受信后端代码样例' : 'Trusted-backend code samples'}`,
        '',
      );
      for (const sample of codeSamples) {
        lines.push(
          `##### ${sample.label ?? sample.lang ?? 'sample'}`,
          '',
          `\`\`\`${sample.lang ?? 'text'}`,
          sample.source ?? '',
          '\`\`\`',
          '',
        );
      }
    }

    const renderedSchemaNames = new Set<string>();
    appendSchemaTree(
      lines,
      document,
      locale,
      requestMedia?.schema,
      locale === 'zh' ? '请求 Schema' : 'Request schema',
      renderedSchemaNames,
    );
    appendExamples(
      lines,
      locale === 'zh' ? '请求示例' : 'Request examples',
      requestMedia,
    );

    lines.push(
      '',
      `#### ${locale === 'zh' ? '响应状态' : 'Response statuses'}`,
      '',
      `| ${locale === 'zh' ? '状态' : 'Status'} | ${locale === 'zh' ? '说明' : 'Description'} |`,
      '| --- | --- |',
      ...responseEntries.map(
        ({ status, response }) =>
          `| \`${status}\` | ${markdownCell(response?.description)} |`,
      ),
    );

    for (const { status, media } of responseEntries) {
      appendSchemaTree(
        lines,
        document,
        locale,
        media?.schema,
        `${locale === 'zh' ? '响应 Schema' : 'Response schema'} (\`${status}\`)`,
        renderedSchemaNames,
      );
      appendExamples(
        lines,
        `${locale === 'zh' ? '响应示例' : 'Response examples'} (\`${status}\`)`,
        media,
      );
    }
  }

  lines.push(
    '',
    `[OpenAPI 3.1 ${page.contractLabel}](${page.contractPath})`,
  );
  return lines.join('\n').replace(/\n{3,}/g, '\n\n');
}
