import openapi from '@/contracts/webhooks.openapi.json';

export const dynamic = 'force-static';

export function GET() {
  return Response.json(openapi);
}
