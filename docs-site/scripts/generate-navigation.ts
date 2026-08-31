import {
  domains,
  isNavigationGroup,
  navigationChildParentSlugs,
  navigationPathSegments,
  type DocumentationDomain,
  type NavigationNode,
  type NavigationPage,
} from '../lib/navigation';

const outputPath = new URL('../NAVIGATION.md', import.meta.url);

function route(domain: DocumentationDomain, slugs: string[]) {
  return `/${['{lang}', domain.key, ...slugs].join('/')}`;
}

function item(
  domain: DocumentationDomain,
  page: NavigationPage,
  slugs: string[],
  indent = '',
) {
  const method = page.method ? ` **${page.method}**` : '';
  return `${indent}- **${page.label.zh} / ${page.label.en}**${method} \`${route(domain, slugs)}\` — ${page.description.zh} / ${page.description.en}`;
}

function appendNodes(
  lines: string[],
  domain: DocumentationDomain,
  nodes: NavigationNode[],
  parentSlugs: string[],
  depth: number,
) {
  for (const node of nodes) {
    const slugs = [...parentSlugs, ...navigationPathSegments(node.slug)];
    lines.push(item(domain, node, slugs, '  '.repeat(depth)));
    if (isNavigationGroup(node)) {
      appendNodes(
        lines,
        domain,
        node.children,
        navigationChildParentSlugs(node, slugs),
        depth + 1,
      );
    }
  }
}

function render() {
  const lines = [
    '# WuKongIM v3 Documentation Navigation',
    '',
    '> Generated from `lib/navigation.ts`. Run `bun run navigation:write` after changing the registry.',
    '',
    'The Chinese and English sites share this information architecture. Replace `{lang}` with `zh` or `en`. Publication is controlled per route: published pages have complete bilingual MDX; planned pages stay visible in navigation but remain outside public indexes.',
    '',
  ];

  for (const domain of domains) {
    lines.push(
      `## ${domain.label.zh} / ${domain.label.en}`,
      '',
      `Route: \`/${'{lang}'}/${domain.key}\``,
      '',
      `${domain.description.zh} / ${domain.description.en}`,
      '',
    );

    for (const page of domain.pages) {
      lines.push(item(domain, page, navigationPathSegments(page.slug)), '');
    }

    appendNodes(lines, domain, domain.groups, [], 0);
    lines.push('');
  }

  return `${lines.join('\n').trim()}\n`;
}

const expected = render();
const write = process.argv.includes('--write');
const check = process.argv.includes('--check');

if (write === check) {
  throw new Error('pass exactly one of --write or --check');
}

if (write) {
  await Bun.write(outputPath, expected);
  console.log('wrote NAVIGATION.md');
} else {
  const actual = await Bun.file(outputPath).text();
  if (actual !== expected) {
    console.error('NAVIGATION.md is stale; run `bun run navigation:write`');
    process.exit(1);
  }
  console.log('NAVIGATION.md matches lib/navigation.ts');
}
