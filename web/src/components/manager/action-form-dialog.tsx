import type { FormEvent, ReactNode } from "react"

import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"

type ActionFormDialogProps = {
  open: boolean
  title: string
  description?: string
  submitLabel: string
  cancelLabel?: string
  pending?: boolean
  error?: string
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
  onOpenChange: (open: boolean) => void
  children: ReactNode
}

export function ActionFormDialog({
  open,
  title,
  description,
  submitLabel,
  cancelLabel = "Cancel",
  pending = false,
  error,
  onSubmit,
  onOpenChange,
  children,
}: ActionFormDialogProps) {
  const intl = useIntl()

  if (!open) {
    return null
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/10 p-4" role="dialog">
      <form className="w-full max-w-md rounded-xl border border-border bg-background p-5 shadow-lg" onSubmit={onSubmit}>
        <div className="text-base font-semibold text-foreground">{title}</div>
        {description ? <p className="mt-2 text-sm text-muted-foreground">{description}</p> : null}
        <div className="mt-4 space-y-3">{children}</div>
        {error ? <p className="mt-3 text-sm text-destructive">{error}</p> : null}
        <div className="mt-5 flex justify-end gap-2">
          <Button onClick={() => onOpenChange(false)} size="sm" type="button" variant="outline">
            {cancelLabel === "Cancel" ? intl.formatMessage({ id: "common.cancel" }) : cancelLabel}
          </Button>
          <Button disabled={pending} size="sm" type="submit">
            {submitLabel}
          </Button>
        </div>
      </form>
    </div>
  )
}
