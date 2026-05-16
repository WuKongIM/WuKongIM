import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import type { ClusterIncident } from "../view-model"

type ClusterIncidentListProps = {
  items: ClusterIncident[]
}

export function ClusterIncidentList({ items }: ClusterIncidentListProps) {
  const intl = useIntl()

  return (
    <SectionCard title={intl.formatMessage({ id: "clusterDashboard.incidents.title" })}>
      {items.length > 0 ? (
        <div className="space-y-3">
          {items.map((item) => (
            <div className="flex items-start justify-between gap-3 rounded-2xl border border-border/80 bg-muted/25 px-3 py-3" key={item.key}>
              <div className="flex items-start gap-2">
                <span className={cn("mt-1.5 size-2 shrink-0 rounded-full", item.severity === "critical" ? "bg-destructive" : "bg-warning")} />
                <div>
                  <p className="text-sm font-medium text-foreground">{item.title}</p>
                  {item.detail ? <p className="mt-1 font-mono text-xs text-muted-foreground">{item.detail}</p> : null}
                </div>
              </div>
              <Button aria-label={item.ariaLabel} asChild size="sm" variant="outline">
                <Link to={item.href}>{intl.formatMessage({ id: "clusterDashboard.incidents.inspect" })}</Link>
              </Button>
            </div>
          ))}
        </div>
      ) : (
        <div className="rounded-2xl border border-primary/25 bg-primary/8 px-4 py-4 text-sm text-muted-foreground">
          {intl.formatMessage({ id: "clusterDashboard.incidents.empty" })}
        </div>
      )}
    </SectionCard>
  )
}
