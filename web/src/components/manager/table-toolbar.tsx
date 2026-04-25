import type { ReactNode } from "react"

import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"

type TableToolbarProps = {
  title?: string
  description?: string
  refreshing?: boolean
  onRefresh?: () => void
  actions?: ReactNode
}

export function TableToolbar({
  title,
  description,
  refreshing = false,
  onRefresh,
  actions,
}: TableToolbarProps) {
  const intl = useIntl()

  return (
    <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
      <div>
        {title ? <div className="text-sm font-semibold text-foreground">{title}</div> : null}
        {description ? <p className="mt-1 text-sm text-muted-foreground">{description}</p> : null}
      </div>
      <div className="flex items-center gap-2">
        {actions}
        {onRefresh ? (
          <Button onClick={onRefresh} size="sm" variant="outline">
            {refreshing
              ? intl.formatMessage({ id: "common.refreshing" })
              : intl.formatMessage({ id: "common.refresh" })}
          </Button>
        ) : null}
      </div>
    </div>
  )
}
