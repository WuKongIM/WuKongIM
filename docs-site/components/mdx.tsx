import defaultMdxComponents from 'fumadocs-ui/mdx';
import type { MDXComponents } from 'mdx/types';
import type { ComponentProps } from 'react';
import {
  ChannelTypeTable,
  CompatibilitySnapshot,
  DeviceFlagTable,
  GoldenPathContract,
  JavaScriptCapabilityMatrix,
  MessageFlagTable,
  ReasonCodeTable,
} from './developer-contracts';
import { ClientProtocolPacketTable } from './client-protocol-contracts';
import { Mermaid } from './mermaid';

function ScrollableTable(props: ComponentProps<'table'>) {
  return (
    <div
      className="relative my-6 overflow-auto prose-no-margin focus-visible:outline-2 focus-visible:outline-offset-2"
      tabIndex={0}
    >
      <table {...props} />
    </div>
  );
}

export function getMDXComponents(components?: MDXComponents) {
  return {
    ...defaultMdxComponents,
    table: ScrollableTable,
    ChannelTypeTable,
    ClientProtocolPacketTable,
    CompatibilitySnapshot,
    DeviceFlagTable,
    GoldenPathContract,
    JavaScriptCapabilityMatrix,
    MessageFlagTable,
    Mermaid,
    ReasonCodeTable,
    ...components,
  } satisfies MDXComponents;
}

export const useMDXComponents = getMDXComponents;

declare global {
  type MDXProvidedComponents = ReturnType<typeof getMDXComponents>;
}
