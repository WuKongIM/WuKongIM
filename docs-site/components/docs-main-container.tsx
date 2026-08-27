'use client';

import { cn } from '@/lib/cn';
import { useDocsPage } from 'fumadocs-ui/layouts/docs/page';
import type { ComponentProps } from 'react';

/** Gives every documentation page one main landmark while retaining the Fumadocs grid contract. */
export function DocsMainContainer({ className, ...props }: ComponentProps<'main'>) {
  const { full } = useDocsPage();

  return (
    <main
      id="nd-page"
      data-full={full}
      {...props}
      className={cn(
        'flex w-full max-w-[900px] flex-col [grid-area:main] mx-auto gap-4 px-4 py-6 md:px-6 md:pt-8 xl:px-8 xl:pt-14',
        full && 'max-w-[1168px]',
        className,
      )}
    />
  );
}
