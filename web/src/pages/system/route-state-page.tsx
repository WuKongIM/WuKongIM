import { useIntl } from "react-intl"
import { Link, useNavigate } from "react-router-dom"

import { DocumentTitle } from "@/app/document-title"
import { Button } from "@/components/ui/button"
import { PageContainer } from "@/components/shell/page-container"
import { defaultAppPath } from "@/lib/navigation"

export function RouteLoadingPage() {
  return (
    <div className="grid min-h-svh place-items-center bg-background p-4 text-foreground">
      <RouteLoadingState />
    </div>
  )
}

export function RouteLoadingState() {
  const intl = useIntl()

  return (
    <div className="m-4 w-[calc(100%-2rem)] max-w-md rounded-xl border border-border bg-card p-6" role="status">
      <div className="flex items-center gap-3">
        <span className="size-2 animate-pulse rounded-full bg-[var(--status-running)]" />
        <span className="text-sm font-semibold">
          {intl.formatMessage({ id: "route.loading.title" })}
        </span>
      </div>
      <p className="mt-2 text-sm text-muted-foreground">
        {intl.formatMessage({ id: "route.loading.description" })}
      </p>
    </div>
  )
}

export function NotFoundPage() {
  const intl = useIntl()

  return (
    <PageContainer className="min-h-full justify-center">
      <DocumentTitle titleMessageId="route.notFound.title" />
      <div className="max-w-xl rounded-xl border border-border bg-card p-6 sm:p-8">
        <div className="font-mono text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">404</div>
        <h1 className="mt-3 text-3xl font-semibold tracking-tight text-foreground">
          {intl.formatMessage({ id: "route.notFound.title" })}
        </h1>
        <p className="mt-3 text-sm leading-6 text-muted-foreground">
          {intl.formatMessage({ id: "route.notFound.description" })}
        </p>
        <Button asChild className="mt-6">
          <Link to={defaultAppPath}>{intl.formatMessage({ id: "route.notFound.return" })}</Link>
        </Button>
      </div>
    </PageContainer>
  )
}

export function RouteErrorPage() {
  const intl = useIntl()
  const navigate = useNavigate()

  return (
    <main className="grid min-h-svh place-items-center bg-background p-4 text-foreground">
      <DocumentTitle titleMessageId="route.error.title" />
      <div className="w-full max-w-xl rounded-xl border border-border bg-card p-6 sm:p-8">
        <div className="font-mono text-xs font-semibold uppercase tracking-[0.2em] text-destructive">
          {intl.formatMessage({ id: "route.error.eyebrow" })}
        </div>
        <h1 className="mt-3 text-3xl font-semibold tracking-tight">
          {intl.formatMessage({ id: "route.error.title" })}
        </h1>
        <p className="mt-3 text-sm leading-6 text-muted-foreground">
          {intl.formatMessage({ id: "route.error.description" })}
        </p>
        <div className="mt-6 flex flex-wrap gap-2">
          <Button onClick={() => navigate(defaultAppPath)}>
            {intl.formatMessage({ id: "route.error.return" })}
          </Button>
          <Button onClick={() => window.location.reload()} variant="outline">
            {intl.formatMessage({ id: "route.error.reload" })}
          </Button>
        </div>
      </div>
    </main>
  )
}
