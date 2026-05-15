import { useIntl } from "react-intl"
import { Link } from "react-router-dom"
import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import type { IncidentItem } from "../view-model"

type IncidentListProps = {
  items: IncidentItem[]
}

export function IncidentList({ items }: IncidentListProps) {
  const intl = useIntl()

  const cardTitle =
    intl.formatMessage({ id: "dashboard.incidents.cardTitle" }) +
    (items.length > 0
      ? " " + intl.formatMessage({ id: "dashboard.incidents.cardCount" }, { count: items.length })
      : "")

  const cardDescription = intl.formatMessage({ id: "dashboard.incidents.cardDescription" })

  return (
    <SectionCard title={cardTitle} description={cardDescription}>
      {items.length > 0 ? (
        <div>
          <div className="space-y-3">
            {items.map((item) => (
              <div
                key={item.key}
                className="flex items-start justify-between gap-3 rounded-2xl border border-border/80 bg-muted/25 px-3 py-3"
              >
                {/* Left side */}
                <div className="flex items-start gap-2">
                  <span
                    className={cn(
                      "mt-1.5 size-2 shrink-0 rounded-full",
                      item.severity === "critical" ? "bg-destructive" : "bg-warning",
                    )}
                  />
                  <div>
                    <p className="text-sm font-medium text-foreground">{item.title}</p>
                    {item.detail && (
                      <p className="mt-1 font-mono text-xs text-muted-foreground">{item.detail}</p>
                    )}
                  </div>
                </div>

                {/* Right side */}
                <Button size="sm" variant="outline" aria-label={item.ariaLabel} asChild>
                  <Link to={item.href}>
                    {intl.formatMessage({ id: "dashboard.incidents.inspect" })}
                  </Link>
                </Button>
              </div>
            ))}
          </div>

          {/* View all link */}
          <div className="mt-3 flex justify-end">
            <Button size="sm" variant="ghost" asChild>
              <Link to="/cluster/diagnostics?tab=trace">
                {intl.formatMessage({ id: "dashboard.incidents.viewAll" })}
              </Link>
            </Button>
          </div>
        </div>
      ) : (
        <div className="rounded-2xl border border-primary/25 bg-primary/8 px-4 py-4">
          <div className="flex items-center gap-2 text-sm font-semibold text-foreground">
            <span className="size-2 rounded-full bg-[var(--status-healthy)]" />
            {intl.formatMessage({ id: "dashboard.noActiveAlertsTitle" })}
          </div>
          <p className="mt-2 text-sm leading-6 text-muted-foreground">
            {intl.formatMessage({ id: "dashboard.noActiveAlertsDescription" })}
          </p>
        </div>
      )}
    </SectionCard>
  )
}
