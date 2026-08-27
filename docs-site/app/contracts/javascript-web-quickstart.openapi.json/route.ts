import openapi from '@/contracts/javascript-web-quickstart.openapi.json';

export const revalidate = false;

export function GET() {
  return Response.json(openapi);
}
