import openapi from '@/contracts/operations-http.openapi.json';

export const dynamic = 'force-static';

export function GET() {
  return Response.json(openapi);
}
