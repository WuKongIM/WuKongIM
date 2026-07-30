import type { Metadata } from 'next';
import type { ReactNode } from 'react';
import '../global.css';

export const metadata: Metadata = {
  title: 'WuKongIM Docs',
  description: 'WuKongIM v3 documentation',
};

export default function RootLanguageLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="zh">
      <body className="min-h-screen bg-fd-background text-fd-foreground">{children}</body>
    </html>
  );
}
