import { createElement, type ReactNode } from 'react';
import {
  BookOpen,
  Bot,
  Compass,
  Rocket,
  Server,
  Smartphone,
  Wrench,
} from 'lucide-react';

const docsIcons = {
  overview: Compass,
  'quick-start': Rocket,
  client: Smartphone,
  server: Server,
  'ai-agent': Bot,
  operations: Wrench,
  resources: BookOpen,
} as const;

export function resolveDocsIcon(icon: string | undefined): ReactNode {
  if (!icon) return undefined;

  const Icon = docsIcons[icon as keyof typeof docsIcons];
  if (!Icon) return undefined;

  return createElement(Icon, {
    className: 'size-4',
    'aria-hidden': 'true',
  });
}
