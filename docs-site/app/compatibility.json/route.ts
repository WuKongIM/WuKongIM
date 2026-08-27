import { compatibilitySnapshot } from '@/lib/developer-contracts';

export const revalidate = false;

export function GET() {
  return Response.json(compatibilitySnapshot);
}
