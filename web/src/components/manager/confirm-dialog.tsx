import type { ReactNode } from "react"

import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"

type ConfirmDialogProps = {
  open: boolean
  title: string
  description?: string
  confirmLabel: string
  cancelLabel?: string
  pending?: boolean
  error?: string
  onConfirm: () => void
  onOpenChange: (open: boolean) => void
  children?: ReactNode
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  cancelLabel = "Cancel",
  pending = false,
  error,
  onConfirm,
  onOpenChange,
  children,
}: ConfirmDialogProps) {
  const intl = useIntl()

  if (!open) {
    return null
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/10 p-4" role="dialog">
      <div className="w-full max-w-md rounded-xl border border-border bg-background p-5 shadow-lg">
        <div className="text-base font-semibold text-foreground">{title}</div>
        {description ? <p className="mt-2 text-sm text-muted-foreground">{description}</p> : null}
        {children ? <div className="mt-4">{children}</div> : null}
        {error ? <p className="mt-3 text-sm text-destructive">{error}</p> : null}
        <div className="mt-5 flex justify-end gap-2">
          <Button onClick={() => onOpenChange(false)} size="sm" variant="outline">
            {cancelLabel === "Cancel" ? intl.formatMessage({ id: "common.cancel" }) : cancelLabel}
          </Button>
          <Button disabled={pending} onClick={onConfirm} size="sm">
            {confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  )
}
