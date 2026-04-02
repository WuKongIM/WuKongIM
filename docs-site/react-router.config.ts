import type { Config } from '@react-router/dev/config';
import { glob } from 'node:fs/promises';
import { getSlugs } from 'fumadocs-core/source';

function toDocUrl(slugs: string[]) {
  return slugs.length === 0 ? '/' : `/${slugs.join('/')}`;
}

export default {
  ssr: false,
  buildDirectory: 'dist',
  future: {
    v8_middleware: true,
  },
  async prerender({ getStaticPaths }) {
    const paths: string[] = [];
    const excluded: string[] = [];

    for (const path of getStaticPaths()) {
      if (!excluded.includes(path)) paths.push(path);
    }

    for await (const entry of glob('**/*.mdx', { cwd: 'content/docs' })) {
      const slugs = getSlugs(entry);
      paths.push(toDocUrl(slugs));
    }

    return paths;
  },
} satisfies Config;
