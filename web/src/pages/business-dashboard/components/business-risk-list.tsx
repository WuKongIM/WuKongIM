import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import { SectionCard } from "@/components/shell/section-card"
import { Button } from "@/components/ui/button"
import type { BusinessRisk } from "../view-model"

type BusinessRiskListProps = {
  risks: BusinessRisk[]
}

export function BusinessRiskList({ risks }: BusinessRiskListProps) {
  const intl = useIntl()
  return (
    <SectionCard title={intl.formatMessage({ id: "businessDashboard.risks.title" })}>
      {risks.length > 0 ? (
        <div className="space-y-3">
          {risks.map((risk) => (
            <div className="flex items-start justify-between gap-3 rounded-2xl border border-border/80 bg-muted/25 px-3 py-3" key={risk.key}>
              <div>
                <p className="text-sm font-medium text-foreground">{intl.formatMessage({ id: risk.titleId })}</p>
                <p className="mt-1 font-mono text-xs text-muted-foreground">{risk.detail}</p>
              </div>
              <Button asChild size="sm" variant="outline"><Link to={risk.href}>{intl.formatMessage({ id: "common.inspect" })}</Link></Button>
            </div>
          ))}
        </div>
      ) : (
        <div className="rounded-2xl border border-primary/25 bg-primary/8 px-4 py-4 text-sm text-muted-foreground">
          {intl.formatMessage({ id: "businessDashboard.risks.empty" })}
        </div>
      )}
    </SectionCard>
  )
}
