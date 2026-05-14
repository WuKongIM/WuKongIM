import type { PropsWithChildren, ReactNode } from "react"
import { useIntl } from "react-intl"
import { useInRouterContext, useLocation } from "react-router-dom"

import { cn } from "@/lib/utils"
import { getActiveNavigationItem } from "@/lib/navigation"

type PageHeaderProps = PropsWithChildren<{
  title: string
  description: string
  eyebrow?: string
  actions?: ReactNode
  className?: string
}>

export function PageHeader({
  title,
  description,
  eyebrow,
  actions,
  className,
  children,
}: PageHeaderProps) {
  const inRouter = useInRouterContext()

  return (
    <section
      className={cn(
        "overflow-hidden rounded-lg border border-border bg-card shadow-none",
        className,
      )}
    >
      <div className="flex flex-col gap-4 p-5 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-2">
          {eyebrow ? <HeaderEyebrow>{eyebrow}</HeaderEyebrow> : null}
          {!eyebrow && inRouter ? <RouteEyebrow /> : null}
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">{title}</h1>
          <p className="max-w-3xl text-sm leading-6 text-muted-foreground">{description}</p>
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
      {children ? <div className="border-t border-border bg-muted/50 p-4">{children}</div> : null}
    </section>
  )
}

function HeaderEyebrow({ children }: PropsWithChildren) {
  return (
    <div className="font-mono text-[11px] font-semibold uppercase tracking-[0.22em] text-muted-foreground">
      {children}
    </div>
  )
}

function RouteEyebrow() {
  const intl = useIntl()
  const location = useLocation()
  const page = getActiveNavigationItem(location.pathname)

  return page ? <HeaderEyebrow>{intl.formatMessage({ id: page.pathLabelMessageId })}</HeaderEyebrow> : null
}
