import { baseOptions } from '@/lib/layout.shared';
import { parseLocale } from '@/lib/navigation';
import { HomeLayout } from 'fumadocs-ui/layouts/home';
import { notFound } from 'next/navigation';
import type { ReactNode } from 'react';

export default async function Layout({
  children,
  params,
}: {
  children: ReactNode;
  params: Promise<{ lang: string }>;
}) {
  const value = (await params).lang;
  const locale = parseLocale(value);
  if (!locale) notFound();

  return <HomeLayout {...baseOptions(locale)}>{children}</HomeLayout>;
}
