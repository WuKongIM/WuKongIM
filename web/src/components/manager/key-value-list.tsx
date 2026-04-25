import type { ReactNode } from "react"

type KeyValueItem = {
  label: string
  value: ReactNode
}

type KeyValueListProps = {
  items: KeyValueItem[]
}

export function KeyValueList({ items }: KeyValueListProps) {
  return (
    <dl className="grid gap-3 sm:grid-cols-2">
      {items.map((item) => (
        <div className="rounded-lg border border-border bg-card px-3 py-3" key={item.label}>
          <dt className="text-xs font-medium uppercase tracking-[0.14em] text-muted-foreground">
            {item.label}
          </dt>
          <dd className="mt-2 text-sm text-foreground">{item.value}</dd>
        </div>
      ))}
    </dl>
  )
}
