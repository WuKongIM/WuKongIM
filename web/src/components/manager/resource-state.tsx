import { useIntl } from "react-intl"

import { Button } from "@/components/ui/button"

type ResourceStateKind = "loading" | "empty" | "forbidden" | "unavailable" | "error"

type ResourceStateProps = {
  kind: ResourceStateKind
  title: string
  description?: string
  retryLabel?: string
  onRetry?: () => void
}

export function ResourceState({
  kind,
  title,
  description,
  retryLabel = "Retry",
  onRetry,
}: ResourceStateProps) {
  const intl = useIntl()

  return (
    <div
      className="rounded-xl border border-border bg-card px-5 py-6 text-sm text-muted-foreground"
      data-kind={kind}
      role="status"
    >
      <div className="text-sm font-semibold text-foreground">{title}</div>
      <p className="mt-2 max-w-2xl leading-6">
        {description ?? intl.formatMessage({ id: `resourceState.${kind}` })}
      </p>
      {onRetry ? (
        <Button className="mt-4" onClick={onRetry} size="sm" variant="outline">
          {retryLabel === "Retry" ? intl.formatMessage({ id: "common.retry" }) : retryLabel}
        </Button>
      ) : null}
    </div>
  )
}
