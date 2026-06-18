import { AlertTriangle, ArrowUpRight, CheckCircle2 } from "lucide-react"
import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import type { BusinessRisk } from "../view-model"

type BusinessRiskListProps = {
  risks: BusinessRisk[]
}

export function BusinessRiskList({ risks }: BusinessRiskListProps) {
  const intl = useIntl()

  return (
    <aside className="rounded-lg border border-border/80 bg-card p-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: "businessDashboard.risks.title" })}</h2>
          <p className="mt-1 text-xs text-muted-foreground">{intl.formatMessage({ id: "businessDashboard.risks.description" })}</p>
        </div>
        <span className="rounded-md border border-border bg-background/70 px-2 py-1 font-mono text-xs text-muted-foreground">
          {risks.length}
        </span>
      </div>
      {risks.length > 0 ? (
        <div className="mt-4 space-y-2">
          {risks.map((risk) => (
            <div className="rounded-lg border border-border/80 bg-background/55 p-3" key={risk.key}>
              <div className="flex items-start gap-2">
                <span
                  className={cn(
                    "mt-0.5 rounded-md border p-1",
                    risk.severity === "critical" ? "border-destructive/30 bg-destructive/10 text-destructive" : "border-warning/30 bg-warning/10 text-warning",
                  )}
                >
                  <AlertTriangle aria-hidden className="size-3.5" />
                </span>
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-foreground">{intl.formatMessage({ id: risk.titleId })}</p>
                  <p className="mt-1 font-mono text-xs text-muted-foreground">
                    {intl.formatMessage({ id: risk.detailId }, risk.detailValues)}
                  </p>
                </div>
              </div>
              <Button asChild className="mt-3 w-full justify-between" size="sm" variant="outline">
                <Link to={risk.href}>
                  {intl.formatMessage({ id: "common.inspect" })}
                  <ArrowUpRight aria-hidden className="size-3.5" />
                </Link>
              </Button>
            </div>
          ))}
        </div>
      ) : (
        <div className="mt-4 flex items-center gap-2 rounded-lg border border-success/25 bg-success/8 px-3 py-3 text-sm text-muted-foreground">
          <CheckCircle2 aria-hidden className="size-4 text-success" />
          <span>{intl.formatMessage({ id: "businessDashboard.risks.empty" })}</span>
        </div>
      )}
    </aside>
  )
}
