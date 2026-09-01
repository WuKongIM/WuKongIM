import { renderMermaidSVG } from 'beautiful-mermaid';
import { CodeBlock, Pre } from 'fumadocs-ui/components/codeblock';

/** Render Mermaid fences with the Beautiful Mermaid integration recommended by Fumadocs. */
export async function Mermaid({ chart }: { chart: string }) {
  let svg: string | undefined;

  try {
    svg = renderMermaidSVG(chart, {
      bg: 'var(--color-fd-background)',
      fg: 'var(--color-fd-foreground)',
      interactive: true,
      transparent: true,
    });
  } catch {
    svg = undefined;
  }

  if (!svg) {
    return (
      <CodeBlock title="Mermaid">
        <Pre>{chart}</Pre>
      </CodeBlock>
    );
  }

  return (
    <div
      className="not-prose my-6 overflow-x-auto rounded-xl border p-4 [&_svg]:mx-auto [&_svg]:h-auto [&_svg]:max-w-none"
      role="img"
      aria-label="Mermaid diagram"
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}
