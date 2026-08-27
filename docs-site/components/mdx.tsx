import defaultMdxComponents from 'fumadocs-ui/mdx';
import type { MDXComponents } from 'mdx/types';
import {
  CompatibilitySnapshot,
  GoldenPathContract,
  ReasonCodeTable,
} from './developer-contracts';

export function getMDXComponents(components?: MDXComponents) {
  return {
    ...defaultMdxComponents,
    CompatibilitySnapshot,
    GoldenPathContract,
    ReasonCodeTable,
    ...components,
  } satisfies MDXComponents;
}

export const useMDXComponents = getMDXComponents;

declare global {
  type MDXProvidedComponents = ReturnType<typeof getMDXComponents>;
}
