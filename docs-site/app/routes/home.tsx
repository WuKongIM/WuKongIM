import type { Route } from './+types/home';
import { DocsRoutePage } from '@/lib/docs-route';
import { loadDocsPage } from '@/lib/docs-route.server';

export async function loader(_: Route.LoaderArgs) {
  return loadDocsPage([]);
}

export default function Page({ loaderData }: Route.ComponentProps) {
  return <DocsRoutePage loaderData={loaderData} />;
}
