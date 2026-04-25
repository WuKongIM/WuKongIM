import { cn } from "@/lib/utils"

type PlaceholderBlockProps = {
  kind?: "panel" | "list" | "table" | "canvas" | "detail"
  className?: string
}

const kindClasses: Record<NonNullable<PlaceholderBlockProps["kind"]>, string> = {
  panel: "min-h-40",
  list: "min-h-36",
  table: "min-h-56",
  canvas: "min-h-[22rem]",
  detail: "min-h-48",
}

export function PlaceholderBlock({
  kind = "panel",
  className,
}: PlaceholderBlockProps) {
  if (kind === "table") {
    return (
      <div
        data-testid="placeholder-table"
        className={cn("rounded-lg border border-border bg-muted/30 p-4", kindClasses[kind], className)}
      >
        <div className="grid grid-cols-4 gap-3 border-b border-border pb-3">
          <span className="h-2 rounded-full bg-foreground/25" />
          <span className="h-2 rounded-full bg-foreground/20" />
          <span className="h-2 rounded-full bg-foreground/20" />
          <span className="h-2 rounded-full bg-foreground/15" />
        </div>
        <div className="mt-4 grid gap-3">
          {Array.from({ length: 3 }).map((_, index) => (
            <div
              key={`table-row-${index}`}
              data-testid="placeholder-table-row"
              className="grid grid-cols-4 gap-3 border-b border-border/70 pb-3 last:border-b-0 last:pb-0"
            >
              <span className="h-2 rounded-full bg-foreground/20" />
              <span className="h-2 rounded-full bg-foreground/15" />
              <span className="h-2 rounded-full bg-foreground/15" />
              <span className="h-2 rounded-full bg-foreground/10" />
            </div>
          ))}
        </div>
      </div>
    )
  }

  if (kind === "list" || kind === "detail") {
    return (
      <div className={cn("rounded-lg border border-border bg-muted/30 p-4", kindClasses[kind], className)}>
        <div className="grid gap-3">
          {Array.from({ length: 3 }).map((_, index) => (
            <div
              key={`${kind}-row-${index}`}
              className="rounded-md border border-border bg-background px-4 py-3"
            >
              <span className="block h-2 w-1/3 rounded-full bg-foreground/20" />
              <span className="mt-3 block h-2 w-3/4 rounded-full bg-foreground/15" />
            </div>
          ))}
        </div>
      </div>
    )
  }

  if (kind === "canvas") {
    return (
      <div
        className={cn(
          "rounded-lg border border-border bg-muted/20 p-4 [background-image:linear-gradient(to_right,rgba(0,0,0,0.06)_1px,transparent_1px),linear-gradient(to_bottom,rgba(0,0,0,0.06)_1px,transparent_1px)] [background-size:24px_24px]",
          kindClasses[kind],
          className,
        )}
      >
        <div className="grid h-full place-items-center rounded-md border border-dashed border-border bg-background/70">
          <div className="space-y-3">
            <span className="block h-2 w-28 rounded-full bg-foreground/20" />
            <span className="block h-2 w-40 rounded-full bg-foreground/10" />
          </div>
        </div>
      </div>
    )
  }

  return (
    <div
      className={cn(
        "rounded-lg border border-border bg-muted/30 p-4",
        kindClasses[kind],
        className,
      )}
    >
      <div className="grid gap-3">
        <span className="block h-2 w-1/4 rounded-full bg-foreground/20" />
        <span className="block h-2 w-2/3 rounded-full bg-foreground/15" />
        <span className="block h-2 w-1/2 rounded-full bg-foreground/10" />
      </div>
    </div>
  )
}
