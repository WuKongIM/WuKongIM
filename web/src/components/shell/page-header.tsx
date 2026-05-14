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
        "overflow-hidden rounded-3xl border border-border/80 bg-[linear-gradient(135deg,rgba(255,255,255,0.07),rgba(255,255,255,0.025))] shadow-[0_24px_80px_rgba(0,0,0,0.18)]",
        className,
      )}
    >
      <div className="flex flex-col gap-5 p-5 lg:flex-row lg:items-start lg:justify-between lg:p-6">
        <div className="space-y-2">
          {eyebrow ? <HeaderEyebrow>{eyebrow}</HeaderEyebrow> : null}
          {!eyebrow && inRouter ? <RouteEyebrow /> : null}
          <h1 className="text-3xl font-semibold tracking-[-0.04em] text-foreground sm:text-4xl">{title}</h1>
          <p className="max-w-3xl text-sm leading-6 text-muted-foreground">{description}</p>
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
      {children ? <div className="border-t border-border/80 bg-background/35 p-4">{children}</div> : null}
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
