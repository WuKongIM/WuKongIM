import openapi from '@/contracts/product-http-messaging.openapi.json';

export const revalidate = false;

export function GET() {
  return Response.json(openapi);
}
