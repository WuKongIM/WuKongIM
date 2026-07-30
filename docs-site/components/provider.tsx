'use client';
import SearchDialog from '@/components/search';
import { translations } from '@/lib/layout.shared';
import type { Locale } from '@/lib/navigation';
import { i18nProvider } from 'fumadocs-ui/i18n';
import { RootProvider } from 'fumadocs-ui/provider/next';
import { type ReactNode } from 'react';

export function Provider({ children, locale }: { children: ReactNode; locale: Locale }) {
  return (
    <RootProvider
      i18n={i18nProvider(translations, locale)}
      search={{ SearchDialog }}
      theme={{ defaultTheme: 'system', enableSystem: true }}
    >
      {children}
    </RootProvider>
  );
}
