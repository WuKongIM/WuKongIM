import { fileURLToPath } from 'node:url';

export const RSC_REFRESH_ORIGIN = 'https://docs.githubim.com';
export const RSC_REFRESH_URLS_FILE = 'cdn-rsc-refresh-urls.txt';
export const RSC_REFRESH_URL_LIMIT = 500;

const safeStaticHTMLRoute = /^(?:[A-Za-z0-9_-]+\/)*index\.html$/;
const safeRSCRefreshURL =
  /^https:\/\/docs\.githubim\.com\/(?:[A-Za-z0-9_-]+\/)*index\.txt$/;

function normalizeOutputPath(path: string): string {
  return path.replaceAll('\\', '/');
}

function compareCodeUnits(left: string, right: string): number {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

/** Maps one eligible static HTML artifact to its physical RSC sibling. */
export function staticHTMLToRSCPayloadPath(path: string): string | null {
  const normalized = normalizeOutputPath(path);
  if (normalized === '404/index.html') return null;
  if (!safeStaticHTMLRoute.test(normalized)) {
    throw new Error(`unsafe static HTML route: ${path}`);
  }
  return `${normalized.slice(0, -'index.html'.length)}index.txt`;
}

/** Builds the exact fixed-origin inventory without accepting arbitrary URLs. */
export function createRSCRefreshURLs(
  staticHTMLPaths: readonly string[],
  staticPayloadPaths: ReadonlySet<string>,
): string[] {
  const normalizedPayloadPaths = new Set(
    [...staticPayloadPaths].map((path) => normalizeOutputPath(path)),
  );
  const seenPayloadPaths = new Set<string>();
  const urls: string[] = [];

  for (const htmlPath of staticHTMLPaths) {
    const payloadPath = staticHTMLToRSCPayloadPath(htmlPath);
    if (payloadPath === null) continue;
    if (seenPayloadPaths.has(payloadPath)) {
      throw new Error(`duplicate static RSC route: ${payloadPath}`);
    }
    seenPayloadPaths.add(payloadPath);
    if (!normalizedPayloadPaths.has(payloadPath)) {
      throw new Error(`missing static RSC sibling: ${payloadPath}`);
    }
    urls.push(`${RSC_REFRESH_ORIGIN}/${payloadPath}`);
  }

  if (urls.length === 0) {
    throw new Error('RSC refresh inventory must contain at least one URL');
  }
  if (urls.length > RSC_REFRESH_URL_LIMIT) {
    throw new Error(
      `RSC refresh inventory has ${urls.length} URLs and exceeds safety limit ${RSC_REFRESH_URL_LIMIT}`,
    );
  }

  return urls.sort(compareCodeUnits);
}

/** Serializes an inventory for direct future use as newline-delimited URLs. */
export function serializeRSCRefreshURLs(urls: readonly string[]): string {
  return `${urls.join('\n')}\n`;
}

/** Reports any way a generated inventory differs from the verified static routes. */
export function findRSCRefreshURLInventoryIssues(
  content: string,
  expectedURLs: readonly string[],
): string[] {
  const issues: string[] = [];
  if (!content.endsWith('\n') || content.endsWith('\n\n')) {
    issues.push('inventory must end with exactly one LF');
  }
  if (content.includes('\r')) {
    issues.push('inventory must use LF line endings');
  }

  const withoutFinalLF = content.endsWith('\n') ? content.slice(0, -1) : content;
  const urls = withoutFinalLF.split('\n');
  if (urls.some((url) => url.length === 0)) {
    issues.push('inventory must not contain blank lines');
  }
  if (urls.length > RSC_REFRESH_URL_LIMIT) {
    issues.push(`inventory exceeds safety limit ${RSC_REFRESH_URL_LIMIT}`);
  }

  for (const url of urls) {
    if (!/^[\x21-\x7e]+$/.test(url) || !safeRSCRefreshURL.test(url)) {
      issues.push(`unsafe RSC refresh URL: ${url || '<empty>'}`);
    }
  }

  if (new Set(urls).size !== urls.length) {
    issues.push('inventory URLs must be unique');
  }
  const sortedURLs = [...urls].sort(compareCodeUnits);
  if (urls.join('\n') !== sortedURLs.join('\n')) {
    issues.push('inventory URLs must use deterministic code-unit ordering');
  }
  if (urls.join('\n') !== expectedURLs.join('\n')) {
    issues.push('inventory URLs do not exactly match eligible static RSC routes');
  }

  return issues;
}

/** Generates the inert inventory inside the already-built static export. */
export async function generateRSCRefreshURLInventory(
  outputDirectory = new URL('../out/', import.meta.url),
): Promise<string[]> {
  const directoryURL = outputDirectory.href.endsWith('/')
    ? outputDirectory
    : new URL(`${outputDirectory.href}/`);
  const staticHTMLPaths: string[] = [];
  const staticPayloadPaths = new Set<string>();
  const cwd = fileURLToPath(directoryURL);

  for await (const filePath of new Bun.Glob('**/*').scan({ cwd, onlyFiles: true })) {
    const normalized = normalizeOutputPath(filePath);
    if (normalized === 'index.html' || normalized.endsWith('/index.html')) {
      staticHTMLPaths.push(normalized);
    }
    if (normalized === 'index.txt' || normalized.endsWith('/index.txt')) {
      staticPayloadPaths.add(normalized);
    }
  }

  const urls = createRSCRefreshURLs(staticHTMLPaths, staticPayloadPaths);
  await Bun.write(
    new URL(RSC_REFRESH_URLS_FILE, directoryURL),
    serializeRSCRefreshURLs(urls),
  );
  return urls;
}

if (import.meta.main) {
  const urls = await generateRSCRefreshURLInventory();
  console.log(`wrote ${RSC_REFRESH_URLS_FILE} with ${urls.length} bounded RSC URLs`);
}
