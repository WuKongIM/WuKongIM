import { Provider } from '@/components/provider';
import { locales, parseLocale } from '@/lib/navigation';
import { siteUrl } from '@/lib/shared';
import type { Metadata } from 'next';
import { notFound } from 'next/navigation';
import type { ReactNode } from 'react';
import '../global.css';

export function generateStaticParams() {
  return locales.map((lang) => ({ lang }));
}

export const metadata: Metadata = {
  metadataBase: new URL(siteUrl),
  title: {
    default: 'WuKongIM Docs',
    template: '%s · WuKongIM Docs',
  },
  description: 'WuKongIM v3 public documentation for developers and operators.',
  applicationName: 'WuKongIM Docs',
};

export default async function LocaleLayout({
  children,
  params,
}: {
  children: ReactNode;
  params: Promise<{ lang: string }>;
}) {
  const locale = parseLocale((await params).lang);
  if (!locale) notFound();

  return (
    <html lang={locale} suppressHydrationWarning>
      <body className="flex min-h-screen flex-col bg-fd-background text-fd-foreground">
        <Provider locale={locale}>{children}</Provider>
      </body>
    </html>
  );
}
