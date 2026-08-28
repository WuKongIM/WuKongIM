import openapi from '@/contracts/product-http.openapi.json';

export const dynamic = 'force-static';

export function GET() {
  return Response.json(openapi);
}
