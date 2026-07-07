import type { PropsWithChildren, ReactNode } from "react"

import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { cn } from "@/lib/utils"

type SectionCardProps = PropsWithChildren<{
  title: string
  description?: string
  action?: ReactNode
  className?: string
  id?: string
}>

export function SectionCard({
  title,
  description,
  action,
  className,
  id,
  children,
}: SectionCardProps) {
  return (
    <Card
      id={id}
      className={cn("border border-border bg-card shadow-none", className)}
    >
      <CardHeader className="border-b border-border bg-background py-3">
        <CardTitle className="font-mono text-xs font-semibold uppercase tracking-[0.14em] text-foreground">{title}</CardTitle>
        {description ? <CardDescription className="leading-6">{description}</CardDescription> : null}
        {action ? <CardAction>{action}</CardAction> : null}
      </CardHeader>
      <CardContent className="pt-4">{children}</CardContent>
    </Card>
  )
}
