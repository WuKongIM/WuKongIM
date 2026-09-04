import { describe, expect, test } from 'bun:test';
import { readdir } from 'node:fs/promises';
import path from 'node:path';
import { renderMermaidSVG } from 'beautiful-mermaid';

const docsRoot = path.join(import.meta.dir, '..', 'content', 'docs');
const mermaidFencePattern = /```mermaid\n([\s\S]*?)\n```/g;

async function mdxFiles(directory: string): Promise<string[]> {
  const entries = await readdir(directory, { withFileTypes: true });
  const nested = await Promise.all(
    entries.map((entry) => {
      const entryPath = path.join(directory, entry.name);
      if (entry.isDirectory()) return mdxFiles(entryPath);
      return entry.name.endsWith('.mdx') ? [entryPath] : [];
    }),
  );
  return nested.flat();
}

describe('Mermaid diagrams', () => {
  test('uses the Fumadocs Beautiful Mermaid integration', async () => {
    const [packageSource, config, mdx, component] = await Promise.all([
      Bun.file(path.join(import.meta.dir, '..', 'package.json')).text(),
      Bun.file(path.join(import.meta.dir, '..', 'source.config.ts')).text(),
      Bun.file(path.join(import.meta.dir, '..', 'components', 'mdx.tsx')).text(),
      Bun.file(path.join(import.meta.dir, '..', 'components', 'mermaid.tsx')).text(),
    ]);
    const packageJson = JSON.parse(packageSource) as {
      dependencies?: Record<string, string>;
    };

    expect(packageJson.dependencies?.['beautiful-mermaid']).toBe('1.1.3');
    expect(config).toContain("from 'fumadocs-core/mdx-plugins'");
    expect(config).toContain(
      'remarkPlugins: [[remarkReleaseVersion, releaseVersion], remarkMdxMermaid]',
    );
    expect(mdx).toContain('Mermaid,');
    expect(component).toContain("from 'beautiful-mermaid'");
    expect(component).toContain("bg: 'var(--color-fd-background)'");
    expect(component).toContain("fg: 'var(--color-fd-foreground)'");
  });

  test('keeps every published Mermaid fence syntactically valid', async () => {
    const files = await mdxFiles(docsRoot);
    const diagramCounts = new Map<string, number>();
    let diagramCount = 0;

    for (const file of files) {
      const source = await Bun.file(file).text();
      const matches = [...source.matchAll(mermaidFencePattern)];
      diagramCounts.set(path.relative(docsRoot, file), matches.length);
      for (const match of matches) {
        diagramCount += 1;
        try {
          renderMermaidSVG(match[1], {
            bg: '#ffffff',
            fg: '#111111',
            transparent: true,
          });
        } catch (error) {
          throw new Error(
            `${path.relative(docsRoot, file)}: ${error instanceof Error ? error.message : String(error)}`,
          );
        }
      }
    }

    expect(diagramCount).toBeGreaterThanOrEqual(40);
    for (const [file, count] of diagramCounts) {
      if (count === 0 || file.endsWith('.en.mdx')) continue;
      expect(diagramCounts.get(file.replace(/\.mdx$/, '.en.mdx'))).toBe(count);
    }
  });
});
