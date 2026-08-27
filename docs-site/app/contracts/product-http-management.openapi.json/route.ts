import openapi from '@/contracts/product-http-management.openapi.json';

export const revalidate = false;

export function GET() {
  return Response.json(openapi);
}
