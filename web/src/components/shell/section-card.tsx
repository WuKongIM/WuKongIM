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
}>

export function SectionCard({
  title,
  description,
  action,
  className,
  children,
}: SectionCardProps) {
  return (
    <Card
      className={cn(
        "border border-border bg-card shadow-none",
        className,
      )}
    >
      <CardHeader className="border-b border-border bg-muted/40">
        <CardTitle className="text-sm font-semibold text-foreground">{title}</CardTitle>
        {description ? <CardDescription className="leading-6">{description}</CardDescription> : null}
        {action ? <CardAction>{action}</CardAction> : null}
      </CardHeader>
      <CardContent className="pt-4">{children}</CardContent>
    </Card>
  )
}
