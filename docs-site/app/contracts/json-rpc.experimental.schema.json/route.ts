import schema from '@/contracts/json-rpc.experimental.schema.json';

export const dynamic = 'force-static';

export function GET() {
  return Response.json(schema);
}
