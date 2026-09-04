import { readFileSync } from 'node:fs';
import path from 'node:path';
import { defineConfig, defineDocs } from 'fumadocs-mdx/config';
import { metaSchema, pageSchema } from 'fumadocs-core/source/schema';
import { remarkMdxMermaid } from 'fumadocs-core/mdx-plugins';
import { remarkReleaseVersion, resolveReleaseVersion } from './lib/release-version';

const releaseVersion = resolveReleaseVersion(
  readFileSync(path.join(process.cwd(), '..', 'CHANGELOG.md'), 'utf8'),
  process.env.DOCS_RELEASE_TAG,
);

export const docs = defineDocs({
  dir: 'content/docs',
  docs: {
    schema: pageSchema,
    postprocess: {
      includeProcessedMarkdown: true,
    },
  },
  meta: {
    schema: metaSchema,
  },
});

export default defineConfig({
  mdxOptions: {
    remarkPlugins: [[remarkReleaseVersion, releaseVersion], remarkMdxMermaid],
  },
});
