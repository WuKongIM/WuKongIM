import { loader } from 'fumadocs-core/source';
import { docs } from 'collections/server';
import { docsRoute } from './shared';
import { resolveDocsIcon } from './icons';

export const source = loader({
  source: docs.toFumadocsSource(),
  baseUrl: docsRoute,
  icon: resolveDocsIcon,
});
