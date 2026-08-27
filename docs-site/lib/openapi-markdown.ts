import openapiDocument from '../contracts/javascript-web-quickstart.openapi.json';
import type { Locale } from './navigation';
import { localizeOpenAPIDocument, productHTTPOpenAPIPages } from './openapi';

interface SchemaObject {
  $ref?: string;
  type?: string;
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
  additionalProperties?: boolean | SchemaObject;
  required?: string[];
  properties?: Record<string, SchemaObject>;
  items?: SchemaObject;
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
  if (schema.type === 'array' && schema.items) {
    return `array<${schemaType(schema.items)}>`;
  }
  return `\`${schema.type ?? 'unknown'}${schema.format ? `:${schema.format}` : ''}\``;
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
    if (typeof resolved.additionalProperties === 'object') visit(resolved.additionalProperties);
  }

  const root = resolveSchema(document, schema);
  for (const field of Object.values(root?.properties ?? {})) visit(field);
  visit(root?.items);
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
) {
  if (!schema) return;
  const name = schemaReferenceName(schema);
  if (name && renderedNames.has(name)) return;
  if (name) renderedNames.add(name);

  lines.push('', `### ${label}${name ? ` — \`${name}\`` : ''}`, '');
  lines.push(...schemaDetails(document, locale, schema));

  for (const referenced of collectReferencedSchemas(document, schema)) {
    if (renderedNames.has(referenced.name)) continue;
    renderedNames.add(referenced.name);
    lines.push(
      '',
      `#### ${locale === 'zh' ? '引用 Schema' : 'Referenced schema'} — \`${referenced.name}\``,
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
) {
  const examples = mediaExamples(media);
  if (examples.length === 0) return;
  lines.push('', `### ${label}`, '');
  for (const example of examples) {
    lines.push(`#### ${example.name}`, '', '```json', JSON.stringify(example.value, null, 2), '```', '');
  }
}

/** Renders one published operation from the same OpenAPI contract used by Fumadocs. */
export function renderOpenAPIOperationMarkdown(locale: Locale, slugs: readonly string[]) {
  if (slugs.length !== 3 || slugs[0] !== 'api' || slugs[1] !== 'product-http') return '';
  const page = productHTTPOpenAPIPages.find((candidate) => candidate.slug === slugs[2]);
  if (!page) return '';

  const document = localizeOpenAPIDocument(
    openapiDocument,
    locale,
  ) as unknown as ContractDocument;
  const operation = document.paths[page.path]?.[page.method];
  if (!operation) return '';

  const requestMedia = operation.requestBody?.content?.['application/json'];
  const responseEntries = Object.entries(operation.responses ?? {}).map(([status, item]) => {
    const response = resolveResponse(document, item);
    return { status, response, media: response?.content?.['application/json'] };
  });
  const lines = [
    `## ${locale === 'zh' ? 'OpenAPI 3.1 操作合同' : 'OpenAPI 3.1 operation contract'}`,
    '',
    locale === 'zh'
      ? '此机器可读摘要与页面中的 Fumadocs 参考来自同一份 Beta 子集合同；Product HTTP 只能由受信后端调用。'
      : 'This machine-readable summary and the Fumadocs reference on the page come from the same Beta subset contract. Product HTTP is callable only from a trusted backend.',
    '',
    `- ${locale === 'zh' ? '方法' : 'Method'}: \`${page.method.toUpperCase()}\``,
    `- ${locale === 'zh' ? '路径' : 'Path'}: \`${page.path}\``,
    `- Operation ID: \`${operation.operationId ?? '—'}\``,
    '',
    operation.description ?? '',
  ];

  const codeSamples = operation['x-codeSamples'] ?? [];
  if (codeSamples.length > 0) {
    lines.push('', `### ${locale === 'zh' ? '受信后端代码样例' : 'Trusted-backend code samples'}`, '');
    for (const sample of codeSamples) {
      lines.push(
        `#### ${sample.label ?? sample.lang ?? 'sample'}`,
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
    `### ${locale === 'zh' ? '响应状态' : 'Response statuses'}`,
    '',
    `| ${locale === 'zh' ? '状态' : 'Status'} | ${locale === 'zh' ? '说明' : 'Description'} |`,
    '| --- | --- |',
    ...responseEntries.map(
      ({ status, response }) => `| \`${status}\` | ${markdownCell(response?.description)} |`,
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

  lines.push(
    '',
    `[OpenAPI 3.1 ${locale === 'zh' ? '子集' : 'subset'}](/contracts/javascript-web-quickstart.openapi.json)`,
  );
  return lines.join('\n').replace(/\n{3,}/g, '\n\n');
}
