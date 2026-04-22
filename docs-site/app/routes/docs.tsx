import type { Route } from './+types/docs';
import { DocsRoutePage } from '@/lib/docs-route';
import { loadDocsPage } from '@/lib/docs-route.server';

export async function loader({ params }: Route.LoaderArgs) {
  const slugs = (params['*'] ?? '').split('/').filter((v) => v.length > 0);
  return loadDocsPage(slugs);
}

export default function Page({ loaderData }: Route.ComponentProps) {
  return <DocsRoutePage loaderData={loaderData} />;
}
