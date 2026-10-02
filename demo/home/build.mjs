import { createHash } from 'node:crypto';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';

// Produce a dependency-free embedded catalog with a content-addressed stylesheet.
const root = new URL('./', import.meta.url);
const output = new URL('../../internal/access/api/demoui/homedist/', root);
const style = await readFile(new URL('style.css', root));
const asset = `home-${createHash('sha256').update(style).digest('hex').slice(0, 12)}.css`;
const html = (await readFile(new URL('index.html', root), 'utf8')).replace('/demos/assets/home.css', `/demos/assets/${asset}`);
await rm(output, { recursive: true, force: true });
await mkdir(new URL('assets/', output), { recursive: true });
await writeFile(new URL(`assets/${asset}`, output), style);
await writeFile(new URL('index.html', output), html);
console.log(`Demo catalog built: ${asset}`);
