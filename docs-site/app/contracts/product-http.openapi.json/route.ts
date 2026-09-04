import openapi from '@/contracts/product-http.openapi.json';
import { localizeOpenAPIDocument } from '@/lib/product-http-openapi';

export const dynamic = 'force-static';

export function GET() {
  return Response.json(localizeOpenAPIDocument(openapi, 'en'));
}
