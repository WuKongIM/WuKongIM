import { loader } from 'fumadocs-core/source';
import { docs } from 'collections/server';
import { docsRoute } from './shared';

export const source = loader({
  source: docs.toFumadocsSource(),
  baseUrl: docsRoute,
});
