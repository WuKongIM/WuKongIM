import defaultMdxComponents from 'fumadocs-ui/mdx';
import type { MDXComponents } from 'mdx/types';
import {
  ChannelTypeTable,
  CompatibilitySnapshot,
  DeviceFlagTable,
  GoldenPathContract,
  JavaScriptCapabilityMatrix,
  MessageFlagTable,
  ReasonCodeTable,
} from './developer-contracts';

export function getMDXComponents(components?: MDXComponents) {
  return {
    ...defaultMdxComponents,
    ChannelTypeTable,
    CompatibilitySnapshot,
    DeviceFlagTable,
    GoldenPathContract,
    JavaScriptCapabilityMatrix,
    MessageFlagTable,
    ReasonCodeTable,
    ...components,
  } satisfies MDXComponents;
}

export const useMDXComponents = getMDXComponents;

declare global {
  type MDXProvidedComponents = ReturnType<typeof getMDXComponents>;
}
