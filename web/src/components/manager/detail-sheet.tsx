import type { ReactNode } from "react"

import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"

type DetailSheetProps = {
  open: boolean
  title: string
  description?: string
  onOpenChange: (open: boolean) => void
  children: ReactNode
  footer?: ReactNode
}

export function DetailSheet({
  open,
  title,
  description,
  onOpenChange,
  children,
  footer,
}: DetailSheetProps) {
  const intl = useIntl()

  if (!open) {
    return null
  }

  const titleId = "manager-detail-sheet-title"

  return (
    <div
      aria-labelledby={titleId}
      className="fixed inset-0 z-40 flex justify-end bg-black/10"
      role="dialog"
    >
      <div className="flex h-full w-full max-w-2xl flex-col border-l border-border bg-background shadow-lg">
        <div className="border-b border-border px-4 py-4 pr-14">
          <h2 className="text-base font-medium text-foreground" id={titleId}>
            {title}
          </h2>
          {description ? <p className="mt-1 text-sm text-muted-foreground">{description}</p> : null}
          <Button
            className="absolute right-3 top-3"
            onClick={() => onOpenChange(false)}
            size="sm"
            type="button"
            variant="outline"
          >
            {intl.formatMessage({ id: "common.close" })}
          </Button>
        </div>
        <div className="flex-1 overflow-y-auto px-4 py-4">{children}</div>
        {footer ? <div className="border-t border-border px-4 py-4">{footer}</div> : null}
      </div>
    </div>
  )
}
